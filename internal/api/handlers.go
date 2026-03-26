package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/chat"
	"github.com/lobo235/homelab-chatbot/internal/database"
	"github.com/lobo235/homelab-chatbot/internal/notify"
)

// Handlers holds dependencies for user-facing HTTP handlers.
type Handlers struct {
	DB                *database.DB
	Auth              *auth.Service
	Chat              *chat.Service
	MCPChat           *chat.MCPClient
	NotifyHub         *notify.Hub
	Log               *slog.Logger
	Version           string
	ContextWindowSize int
}

// HandleLogin processes POST /api/auth/login.
func (h *Handlers) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "Username and password are required")
		return
	}

	token, user, err := h.Auth.Login(body.Username, body.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid username or password")
		return
	}

	auth.SetSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": map[string]interface{}{
			"id":             user.ID,
			"username":       user.Username,
			"role":           user.Role,
			"verbosity_mode": user.VerbosityMode,
		},
	})
}

// HandleLogout processes POST /api/auth/logout.
func (h *Handlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(auth.CookieName)
	if err == nil {
		_ = h.Auth.Logout(cookie.Value)
	}
	auth.ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// HandleChat processes POST /api/chat — streams SSE response.
func (h *Handlers) HandleChat(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	var req chat.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}

	isContinuation := strings.TrimSpace(req.Message) == "" && req.ConversationID > 0
	if !isContinuation && strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "Message is required")
		return
	}

	conv, err := h.resolveConversation(w, user, &req)
	if err != nil {
		return
	}

	if conv.InputTokens >= int64(user.MaxTokens) {
		writeError(w, http.StatusTooManyRequests, "token_limit",
			fmt.Sprintf("Token limit reached (%d/%d). Please start a new conversation to reset usage.", conv.InputTokens, user.MaxTokens))
		return
	}

	// For continuations after rate limit pause, add a "please continue" message
	// so the conversation ends with a user message (required by the API).
	msg := req.Message
	if isContinuation {
		msg = "Please continue where you left off."
	}
	if _, err := h.DB.AddMessage(conv.ID, "user", msg); err != nil {
		h.Log.Error("store message", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Failed to store message")
		return
	}

	anthropicMsgs, err := h.buildMessages(conv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to load messages")
		return
	}

	tools := h.getMCPTools()
	verbosity := user.VerbosityMode
	if req.VerbosityMode != "" {
		verbosity = req.VerbosityMode
	}

	// Build owned server list for system prompt scoping.
	var ownedServerNames []string
	if ownedServers, err := h.DB.ListServersByOwner(user.ID); err == nil {
		for _, s := range ownedServers {
			ownedServerNames = append(ownedServerNames, s.ServerName)
		}
	}

	// Build pending async ops context for injection into the system prompt.
	var asyncContext string
	if pendingOps, err := h.DB.ListPendingOps(conv.ID); err == nil && len(pendingOps) > 0 {
		var sb strings.Builder
		sb.WriteString("Pending async operations for this conversation:\n")
		for _, op := range pendingOps {
			fmt.Fprintf(&sb, "- %s ID %q on server %q (%s) — use get_download_status to check\n", op.ToolName, op.OperationID, op.ServerName, op.Description)
		}
		asyncContext = sb.String()
	}

	// Clean up old ops periodically (best-effort, non-blocking).
	go func() { _ = h.DB.CleanOldOps() }()

	h.streamSSE(w, r, conv.ID, anthropicMsgs, tools, verbosity, req.Debug && user.Role == "admin", user, ownedServerNames, asyncContext)
}

// resolveConversation gets an existing conversation or creates a new one.
// On error, it writes the HTTP error response and returns a nil conversation.
func (h *Handlers) resolveConversation(w http.ResponseWriter, user *database.User, req *chat.Request) (*database.Conversation, error) {
	if req.ConversationID > 0 {
		conv, err := h.DB.GetConversation(req.ConversationID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "Conversation not found")
			return nil, err
		}
		if conv.UserID != user.ID {
			writeError(w, http.StatusForbidden, "forbidden", "Access denied")
			return nil, fmt.Errorf("forbidden")
		}
		return conv, nil
	}

	title := req.Message
	if len(title) > 50 {
		title = title[:50] + "..."
	}
	conv, err := h.DB.CreateConversation(user.ID, title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to create conversation")
		return nil, err
	}
	return conv, nil
}

func (h *Handlers) buildMessages(convID int64) ([]chat.AnthropicMessage, error) {
	messages, err := h.DB.GetMessages(convID)
	if err != nil {
		return nil, err
	}

	anthropicMsgs := make([]chat.AnthropicMessage, 0, len(messages))
	for _, m := range messages {
		anthropicMsgs = append(anthropicMsgs, chat.AnthropicMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return anthropicMsgs, nil
}

func (h *Handlers) getMCPTools() []chat.AnthropicToolDef {
	if h.MCPChat == nil {
		return nil
	}
	return chat.MCPToolsToAnthropic(h.MCPChat.GetTools())
}

// maxToolRounds limits the number of tool execution round-trips to prevent runaway loops.
const maxToolRounds = 8

// sseDeadline is the max duration for a single SSE connection. If the tool loop
// is still running when we approach this limit, we pause and let the frontend
// auto-continue with a new request. Set conservatively under typical proxy timeouts.
const sseDeadline = 90 * time.Second

func (h *Handlers) streamSSE(w http.ResponseWriter, r *http.Request, convID int64, msgs []chat.AnthropicMessage, tools []chat.AnthropicToolDef, verbosity string, debugEnabled bool, user *database.User, ownedServers []string, extraContext string) {
	isAdmin := user.Role == "admin"
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Conversation-ID", strconv.FormatInt(convID, 10))

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "Streaming not supported")
		return
	}

	sendEvent := func(event chat.SSEEvent) {
		// Only send debug events when debug mode is enabled by an admin.
		if event.Type == "debug" && !debugEnabled {
			return
		}
		data, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	deadline := time.Now().Add(sseDeadline)
	var fullText strings.Builder
	var totalInputTokens int64

	for round := 0; round < maxToolRounds; round++ {
		// If we're approaching the connection deadline, save progress and
		// tell the frontend to auto-continue with a new request.
		if round > 0 && time.Until(deadline) < 15*time.Second {
			h.Log.Warn("SSE deadline approaching, pausing for frontend continuation",
				"conversation_id", convID, "round", round, "remaining", time.Until(deadline))
			h.savePartialProgress(convID, fullText.String(), totalInputTokens)
			sendEvent(chat.SSEEvent{
				Type:       "rate_limit_pause",
				Message:    "Processing took too long. Continuing automatically...",
				RetryAfter: 2,
			})
			sendEvent(chat.SSEEvent{Type: "done"})
			return
		}

		// Trim context to keep token usage manageable.
		preTrimCount := len(msgs)
		msgs = h.trimContext(msgs)
		if len(msgs) < preTrimCount {
			sendEvent(chat.SSEEvent{
				Type:    "debug",
				Message: fmt.Sprintf("context_trimmed from=%d to=%d", preTrimCount, len(msgs)),
			})
		}

		eventCh := make(chan chat.SSEEvent, 100)
		var result *chat.StreamResult
		var streamErr error

		go func() {
			result, streamErr = h.Chat.StreamResponse(r.Context(), msgs, tools, verbosity, false, ownedServers, isAdmin, eventCh, extraContext)
			close(eventCh)
		}()

		// Forward SSE events to the client as they arrive.
		for event := range eventCh {
			sendEvent(event)
		}

		if streamErr != nil {
			h.handleStreamError(streamErr, convID, round, fullText.String(), sendEvent)
			return
		}

		fullText.WriteString(result.Text)
		if result.InputTokens > totalInputTokens {
			totalInputTokens = result.InputTokens
		}

		// Send debug metadata about this API round.
		sendEvent(chat.SSEEvent{
			Type:    "debug",
			Message: fmt.Sprintf("round=%d input_tokens=%d stop_reason=%s msg_count=%d", round, result.InputTokens, result.StopReason, len(msgs)),
		})

		// If Claude didn't request tool use, we're done.
		if result.StopReason != "tool_use" || len(result.ToolUses) == 0 {
			break
		}

		// Enforce one-tool-at-a-time: only include the first tool_use.
		// Claude sometimes sends multiple tool_use blocks in parallel despite
		// system prompt instructions. We drop the extras to prevent timeout
		// from executing many sequential gateway calls.
		toolUses := result.ToolUses
		if len(toolUses) > 1 {
			h.Log.Warn("claude sent multiple tool calls, limiting to first",
				"requested", len(toolUses), "conversation_id", convID)
			sendEvent(chat.SSEEvent{
				Type:    "debug",
				Message: fmt.Sprintf("tool_limit: claude requested %d tools, executing only first", len(toolUses)),
			})
			toolUses = toolUses[:1]
		}

		// Build the assistant message with text + tool_use content blocks.
		assistantContent := []interface{}{}
		if result.Text != "" {
			assistantContent = append(assistantContent, map[string]string{
				"type": "text",
				"text": result.Text,
			})
		}
		for _, tu := range toolUses {
			assistantContent = append(assistantContent, map[string]interface{}{
				"type":  "tool_use",
				"id":    tu.ID,
				"name":  tu.Name,
				"input": tu.Input,
			})
		}
		msgs = append(msgs, chat.AnthropicMessage{
			Role:    "assistant",
			Content: assistantContent,
		})

		// Execute each tool and collect results.
		toolResults := []interface{}{}
		for _, tu := range toolUses {
			result, _ := h.executeTool(r.Context(), tu, convID, user, &ownedServers, sendEvent)
			resultStr := truncateToolResult(fmt.Sprint(result), sendEvent)

			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": tu.ID,
				"content":     resultStr,
			})
		}

		// Append the user message with tool results.
		msgs = append(msgs, chat.AnthropicMessage{
			Role:    "user",
			Content: toolResults,
		})
	}

	sendEvent(chat.SSEEvent{Type: "done"})
	h.savePartialProgress(convID, fullText.String(), totalInputTokens)
}

// extractDownloadID parses a tool result JSON string for a download_id or backup ID field.
func extractOperationID(toolResult string) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(toolResult), &parsed); err != nil {
		return ""
	}
	// download_to_server returns {"id": "..."} or {"download_id": "..."}
	// trigger_modpack_discovery returns {"operation_id": "..."}
	if id, ok := parsed["id"].(string); ok && id != "" {
		return id
	}
	if id, ok := parsed["download_id"].(string); ok && id != "" {
		return id
	}
	if id, ok := parsed["operation_id"].(string); ok && id != "" {
		return id
	}
	return ""
}

// extractServerFromResult parses a provision result for the server name.
func extractServerFromResult(toolResult string) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(toolResult), &parsed); err != nil {
		return ""
	}
	if srv, ok := parsed["server"].(string); ok && srv != "" {
		return srv
	}
	return ""
}

// extractDownloadStatus parses a get_download_status result for terminal status.
func extractDownloadStatus(toolResult string) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(toolResult), &parsed); err != nil {
		return ""
	}
	if status, ok := parsed["status"].(string); ok {
		if status == "done" || status == "failed" {
			return status
		}
	}
	return ""
}

// executeTool runs a single MCP tool call with pre-execution limit checks and
// post-execution ownership tracking. It returns the tool result and status.
func (h *Handlers) executeTool(ctx context.Context, tu chat.ToolUseBlock, convID int64, user *database.User, ownedServers *[]string, sendEvent func(chat.SSEEvent)) (interface{}, string) {
	var args map[string]interface{}
	if err := json.Unmarshal(tu.Input, &args); err != nil {
		args = map[string]interface{}{}
	}

	// Enforce max_servers limit before executing creation tools.
	if isCreationTool(tu.Name) && user.Role != "admin" {
		count, _ := h.DB.CountServersByOwner(user.ID)
		if count >= user.MaxServers {
			h.Log.Warn("server limit reached", "user", user.ID, "count", count, "max", user.MaxServers)
			result := fmt.Sprintf("Error: You have reached your server limit (%d/%d). Ask an admin to increase your limit.", count, user.MaxServers)
			sendEvent(chat.SSEEvent{Type: "tool_done", Name: tu.Name, Status: "failed"})
			return result, "failed"
		}
	}

	// Inject user context for MCP-level authorization.
	args["_user_id"] = user.ID
	args["_user_role"] = user.Role
	args["_owned_servers"] = strings.Join(*ownedServers, ",")

	h.Log.Info("executing MCP tool", "tool", tu.Name, "conversation_id", convID)
	toolStart := time.Now()
	toolResult, err := h.MCPChat.CallTool(ctx, tu.Name, args)
	toolDuration := time.Since(toolStart)

	status := "done"
	if err != nil {
		status = "failed"
		h.Log.Error("tool execution failed", "tool", tu.Name, "error", err)
		toolResult = fmt.Sprintf("Error: %s", err.Error())
	}

	sendEvent(chat.SSEEvent{
		Type:    "debug",
		Message: fmt.Sprintf("tool=%s duration=%dms status=%s result_len=%d", tu.Name, toolDuration.Milliseconds(), status, len(fmt.Sprint(toolResult))),
	})
	sendEvent(chat.SSEEvent{Type: "tool_done", Name: tu.Name, Status: status})

	// Track ownership lifecycle after successful tool execution.
	if status == "done" {
		h.trackOwnership(tu.Name, args, user.ID, ownedServers)
		h.trackAsyncOps(tu.Name, args, toolResult, convID, user.ID)
	}

	return toolResult, status
}

// trackOwnership records or removes server ownership based on tool name.
func (h *Handlers) trackOwnership(toolName string, args map[string]interface{}, userID int64, ownedServers *[]string) {
	switch toolName {
	case "provision_minecraft_server":
		// Only record ownership on actual provisioning, not on create_minecraft_server
		// which only gathers reference specs.
		serverName, _ := args["name"].(string)
		if serverName == "" {
			serverName, _ = args["server_name"].(string)
		}
		if serverName != "" {
			if err := h.DB.CreateServerOwnership(serverName, userID); err != nil {
				h.Log.Warn("failed to record server ownership", "server", serverName, "user", userID, "error", err)
			} else {
				h.Log.Info("server ownership recorded", "server", serverName, "user", userID)
				*ownedServers = append(*ownedServers, serverName)
			}
		}
	case "destroy_minecraft_server", "destroy_minecraft_server_by_name":
		serverName, _ := args["name"].(string)
		if serverName != "" {
			if err := h.DB.DeleteServerOwnership(serverName); err != nil {
				h.Log.Warn("failed to remove server ownership", "server", serverName, "error", err)
			} else {
				h.Log.Info("server ownership removed", "server", serverName)
			}
		}
	case "rename_minecraft_server":
		oldName, _ := args["old_name"].(string)
		newName, _ := args["new_name"].(string)
		if oldName != "" && newName != "" {
			_ = h.DB.DeleteServerOwnership(oldName)
			if err := h.DB.CreateServerOwnership(newName, userID); err != nil {
				h.Log.Warn("failed to record renamed server ownership", "old", oldName, "new", newName, "error", err)
			} else {
				h.Log.Info("server ownership renamed", "old", oldName, "new", newName, "user", userID)
				// Update in-flight owned servers list.
				for i, s := range *ownedServers {
					if s == oldName {
						(*ownedServers)[i] = newName
						break
					}
				}
			}
		}
	}
}

// trackAsyncOps records or updates async operations based on tool results.
func (h *Handlers) trackAsyncOps(toolName string, args map[string]interface{}, toolResult string, convID, userID int64) {
	resultStr := fmt.Sprint(toolResult)
	serverName, _ := args["server_name"].(string)
	if serverName == "" {
		serverName, _ = args["name"].(string)
	}

	switch toolName {
	case "download_to_server":
		h.trackAsyncDownload(resultStr, args, serverName, convID, userID)
	case "create_backup":
		h.trackAsyncCreate(resultStr, "create_backup", serverName, "creating backup", convID, userID)
	case "trigger_modpack_discovery":
		h.trackAsyncDiscovery(resultStr, args, convID, userID)
	case "provision_minecraft_server":
		h.trackAsyncProvision(resultStr, convID, userID)
	case "destroy_minecraft_server":
		h.trackAsyncDestroy(resultStr, serverName, convID, userID)
	case "get_download_status":
		h.trackDownloadStatusUpdate(resultStr, args)
	}
}

// notifyAsyncStarted sends an immediate async_started event to the user's notification
// stream so toast notifications appear instantly instead of waiting for the next poller tick.
func (h *Handlers) notifyAsyncStarted(userID int64, toolName, opID, serverName, desc string, convID int64) {
	h.NotifyHub.Send(userID, notify.Event{
		Type:        "async_started",
		OpID:        opID,
		OpType:      opTypeFromTool(toolName),
		ServerName:  serverName,
		Description: desc,
		Status:      "pending",
		ConvID:      convID,
	})
}

func (h *Handlers) trackAsyncDownload(resultStr string, args map[string]interface{}, serverName string, convID, userID int64) {
	opID := extractOperationID(resultStr)
	if opID == "" {
		return
	}
	desc := "downloading file"
	if url, ok := args["url"].(string); ok && url != "" {
		desc = "downloading " + url
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}
	}
	if err := h.DB.CreateAsyncOp(convID, userID, "download_to_server", opID, serverName, desc); err != nil {
		h.Log.Warn("failed to track async download", "op_id", opID, "error", err)
		return
	}
	h.notifyAsyncStarted(userID, "download_to_server", opID, serverName, desc, convID)
}

func (h *Handlers) trackAsyncCreate(resultStr, toolName, serverName, desc string, convID, userID int64) {
	opID := extractOperationID(resultStr)
	if opID == "" {
		return
	}
	if err := h.DB.CreateAsyncOp(convID, userID, toolName, opID, serverName, desc); err != nil {
		h.Log.Warn("failed to track async op", "op_id", opID, "error", err)
		return
	}
	h.notifyAsyncStarted(userID, toolName, opID, serverName, desc, convID)
}

func (h *Handlers) trackAsyncDiscovery(resultStr string, args map[string]interface{}, convID, userID int64) {
	opID := extractOperationID(resultStr)
	if opID == "" {
		return
	}
	packName, _ := args["pack_name"].(string)
	if packName == "" {
		packName = opID
	}
	desc := "Learning how to deploy " + packName
	if err := h.DB.CreateAsyncOp(convID, userID, "trigger_modpack_discovery", opID, "", desc); err != nil {
		h.Log.Warn("failed to track async discovery", "op_id", opID, "error", err)
		return
	}
	h.notifyAsyncStarted(userID, "trigger_modpack_discovery", opID, "", desc, convID)
}

func (h *Handlers) trackAsyncProvision(resultStr string, convID, userID int64) {
	srvName := extractServerFromResult(resultStr)
	if srvName == "" {
		return
	}
	desc := "Starting " + srvName
	if err := h.DB.CreateAsyncOp(convID, userID, "provision_minecraft_server", srvName, srvName, desc); err != nil {
		h.Log.Warn("failed to track async provision", "server", srvName, "error", err)
		return
	}
	h.notifyAsyncStarted(userID, "provision_minecraft_server", srvName, srvName, desc, convID)
}

func (h *Handlers) trackAsyncDestroy(resultStr, serverName string, convID, userID int64) {
	srvName := extractServerFromResult(resultStr)
	if srvName == "" {
		srvName = serverName
	}
	if srvName == "" {
		return
	}
	desc := "Destroying " + srvName
	if err := h.DB.CreateAsyncOp(convID, userID, "destroy_minecraft_server", srvName, srvName, desc); err != nil {
		h.Log.Warn("failed to track async destroy", "server", srvName, "error", err)
		return
	}
	h.notifyAsyncStarted(userID, "destroy_minecraft_server", srvName, srvName, desc, convID)
}

func (h *Handlers) trackDownloadStatusUpdate(resultStr string, args map[string]interface{}) {
	downloadID, _ := args["download_id"].(string)
	if downloadID == "" {
		return
	}
	if terminalStatus := extractDownloadStatus(resultStr); terminalStatus != "" {
		if err := h.DB.UpdateAsyncOpStatus(downloadID, terminalStatus); err != nil {
			h.Log.Warn("failed to update async op status", "op_id", downloadID, "error", err)
		}
	}
}

// isCreationTool returns true if the tool name actually provisions a new server.
// create_minecraft_server only gathers reference specs — it doesn't create anything.
func isCreationTool(name string) bool {
	return name == "provision_minecraft_server"
}

// savePartialProgress persists any accumulated assistant text and token usage.
func (h *Handlers) savePartialProgress(convID int64, text string, inputTokens int64) {
	if text != "" {
		if _, err := h.DB.AddMessage(convID, "assistant", text); err != nil {
			h.Log.Error("store assistant message", "error", err)
		}
	}
	if inputTokens > 0 {
		if err := h.DB.UpdateConversationTokens(convID, inputTokens); err != nil {
			h.Log.Error("update tokens", "error", err)
		}
	}
}

// handleStreamError processes errors from StreamResponse, sending the appropriate SSE event.
func (h *Handlers) handleStreamError(streamErr error, convID int64, round int, _ string, sendEvent func(chat.SSEEvent)) {
	// If the API wants a long wait, tell frontend to auto-retry after countdown.
	// Don't save partial assistant text — it's mid-tool-loop and would leave the
	// conversation ending with an assistant message, which the API rejects.
	var rlWait *chat.ErrRateLimitWait
	if errors.As(streamErr, &rlWait) {
		h.Log.Warn("rate limit pause, deferring to frontend", "retry_after", rlWait.RetryAfter, "conversation_id", convID, "round", round)
		sendEvent(chat.SSEEvent{
			Type:       "rate_limit_pause",
			Message:    fmt.Sprintf("Rate limited by API. Auto-retrying in %d seconds...", rlWait.RetryAfter),
			RetryAfter: rlWait.RetryAfter,
		})
		sendEvent(chat.SSEEvent{Type: "done"})
		return
	}

	h.Log.Error("stream error", "error", streamErr, "conversation_id", convID, "round", round)
	errMsg := "Failed to get response from Claude"
	if errors.Is(streamErr, chat.ErrRateLimitExhausted) {
		errMsg = "Rate limit exceeded. Please wait a minute or two and try again."
	}
	sendEvent(chat.SSEEvent{Type: "error", Message: errMsg})
}

// HandleListSessions processes GET /api/sessions.
func (h *Handlers) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	convs, err := h.DB.ListConversations(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to list sessions")
		return
	}

	type sessionItem struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		CreatedAt   string `json:"created_at"`
		LastMessage string `json:"last_message"`
	}

	items := make([]sessionItem, 0, len(convs))
	for _, c := range convs {
		lastMsg := ""
		msg, err := h.DB.GetLastMessage(c.ID)
		if err == nil {
			lastMsg = msg.Content
			if len(lastMsg) > 100 {
				lastMsg = lastMsg[:100] + "..."
			}
		}
		items = append(items, sessionItem{
			ID:          c.ID,
			Title:       c.Title,
			CreatedAt:   c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			LastMessage: lastMsg,
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// HandleGetSession processes GET /api/sessions/{id}.
func (h *Handlers) HandleGetSession(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid session ID")
		return
	}

	conv, err := h.DB.GetConversation(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Session not found")
		return
	}
	if conv.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden", "Not your session")
		return
	}

	msgs, err := h.DB.GetMessages(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to load messages")
		return
	}

	type messageItem struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	}

	msgItems := make([]messageItem, 0, len(msgs))
	for _, m := range msgs {
		msgItems = append(msgItems, messageItem{
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":           conv.ID,
		"title":        conv.Title,
		"input_tokens": conv.InputTokens,
		"messages":     msgItems,
	})
}

// HandleDeleteSession processes DELETE /api/sessions/{id}.
func (h *Handlers) HandleDeleteSession(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid session ID")
		return
	}

	conv, err := h.DB.GetConversation(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Session not found")
		return
	}
	if conv.UserID != user.ID && user.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden", "Not your session")
		return
	}

	if err := h.DB.DeleteConversation(id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to delete session")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleListServers processes GET /api/servers.
func (h *Handlers) HandleListServers(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var servers []*database.ServerOwnership
	var err error
	if user.Role == "admin" {
		servers, err = h.DB.ListAllServers()
	} else {
		servers, err = h.DB.ListServersByOwner(user.ID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to list servers")
		return
	}

	type serverItem struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		Owner     string `json:"owner"`
		Address   string `json:"address"`
		Players   int    `json:"players"`
		CreatedAt string `json:"created_at"`
	}

	items := make([]serverItem, 0, len(servers))
	for _, s := range servers {
		owner := ""
		if u, err := h.DB.GetUserByID(s.OwnerID); err == nil {
			owner = u.Username
		}
		items = append(items, serverItem{
			Name:      s.ServerName,
			Status:    "unknown", // Would be enriched from Nomad in production
			Owner:     owner,
			Address:   s.ServerName + ".example.com",
			Players:   0,
			CreatedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// HandleHealth processes GET /health.
func (h *Handlers) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": h.Version,
	})
}

// HandleGetMe processes GET /api/auth/me — returns current user info.
func (h *Handlers) HandleGetMe(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":             user.ID,
		"username":       user.Username,
		"role":           user.Role,
		"verbosity_mode": user.VerbosityMode,
		"max_servers":    user.MaxServers,
		"max_tokens":     user.MaxTokens,
	})
}

// truncateToolResult caps a tool result string at maxToolResultLen bytes to prevent
// token spikes from large outputs (e.g., 50KB logs). Truncates at a rune boundary.
func truncateToolResult(s string, sendEvent func(chat.SSEEvent)) string {
	const maxToolResultLen = 8192
	if len(s) <= maxToolResultLen {
		return s
	}
	truncated := s[:maxToolResultLen]
	for len(truncated) > 0 && !utf8.Valid([]byte(truncated)) {
		truncated = truncated[:len(truncated)-1]
	}
	sendEvent(chat.SSEEvent{
		Type:    "debug",
		Message: fmt.Sprintf("tool_result truncated from %d to %d bytes", len(s), len(truncated)),
	})
	return truncated + "\n... [truncated — " + fmt.Sprintf("%d", len(s)-len(truncated)) + " bytes omitted]"
}

// trimContext applies a sliding window to keep API token usage bounded.
// It keeps the first user message (for context) plus the last ContextWindowSize
// messages. Messages in between are dropped entirely. tool_use / tool_result
// pairs are never split.
func (h *Handlers) trimContext(msgs []chat.AnthropicMessage) []chat.AnthropicMessage {
	windowSize := h.ContextWindowSize
	if windowSize <= 0 {
		windowSize = 20
	}

	if len(msgs) <= windowSize {
		return msgs
	}

	// Find the boundary: keep first message + last windowSize messages.
	// Walk backward from the cut point to avoid splitting tool_use/tool_result pairs.
	cutIdx := len(msgs) - windowSize
	if cutIdx <= 1 {
		return msgs
	}

	// Ensure we don't split a tool_use from its tool_result. If the message at
	// cutIdx is a user message with tool_result content, include the preceding
	// assistant message (which contains the tool_use) too.
	for cutIdx > 1 && isToolResultMessage(msgs[cutIdx]) {
		cutIdx--
	}

	// Build result: first message + recent window. Middle messages are dropped.
	result := make([]chat.AnthropicMessage, 0, 1+len(msgs)-cutIdx)
	result = append(result, msgs[0])
	result = append(result, msgs[cutIdx:]...)

	return result
}

// isToolResultMessage checks if a message contains tool_result content blocks.
func isToolResultMessage(msg chat.AnthropicMessage) bool {
	arr, ok := msg.Content.([]interface{})
	if !ok {
		return false
	}
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "tool_result" {
			return true
		}
	}
	return false
}

// maxAutoContinuationsPerHour limits automatic continuations per conversation to prevent token runaway.
const maxAutoContinuationsPerHour = 3

// HandleNotifications serves persistent SSE notifications for async operation updates.
func (h *Handlers) HandleNotifications(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "sse_unsupported", "Streaming not supported")
		return
	}

	// Disable write deadline for this long-lived connection.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		h.Log.Warn("failed to disable write deadline for notifications", "error", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	client := h.NotifyHub.Register(user.ID)
	defer h.NotifyHub.Unregister(client)

	h.Log.Debug("notification stream opened", "user_id", user.ID)

	// Send initial state: all pending ops for this user.
	if pendingOps, err := h.DB.ListPendingOpsByUser(user.ID); err == nil {
		for _, op := range pendingOps {
			event := notify.Event{
				Type:        "async_started",
				OpID:        op.OperationID,
				OpType:      opTypeFromTool(op.ToolName),
				ServerName:  op.ServerName,
				Description: op.Description,
				Status:      "pending",
				ConvID:      op.ConversationID,
			}
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()
	}

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case event, ok := <-client.Events:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-keepalive.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			h.Log.Debug("notification stream closed", "user_id", user.ID)
			return
		}
	}
}

// HandleAsyncCompletion is the callback invoked by the poller when an async operation finishes.
// It stores a system message in the conversation and tells the frontend to auto-continue.
func (h *Handlers) HandleAsyncCompletion(op database.AsyncOp, terminalStatus string) {
	if !h.NotifyHub.HasClients(op.UserID) {
		return // user offline, they'll see it on next message via pending ops context
	}

	// Rate-limit auto-continuations to prevent token runaway.
	count, err := h.DB.CountRecentAutoContinuations(op.ConversationID, time.Now().Add(-1*time.Hour))
	if err != nil {
		h.Log.Warn("failed to count auto-continuations", "error", err)
	}
	if count >= maxAutoContinuationsPerHour {
		h.Log.Info("skipping auto-continuation, limit reached",
			"conversation_id", op.ConversationID,
			"count", count,
		)
		return
	}

	statusText := "completed successfully"
	if terminalStatus == "failed" {
		statusText = "failed"
	}
	msg := fmt.Sprintf("[System] %s on server %s %s", op.Description, op.ServerName, statusText)

	if _, err := h.DB.AddMessage(op.ConversationID, "user", msg); err != nil {
		h.Log.Error("failed to add auto-continuation message", "error", err)
		return
	}

	h.NotifyHub.Send(op.UserID, notify.Event{
		Type:    "auto_continue",
		OpID:    op.OperationID,
		ConvID:  op.ConversationID,
		Message: msg,
	})
}

func opTypeFromTool(toolName string) string {
	switch toolName {
	case "download_to_server":
		return "download"
	case "create_backup":
		return "backup"
	default:
		return toolName
	}
}
