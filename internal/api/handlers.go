package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/chat"
	"github.com/lobo235/homelab-chatbot/internal/database"
)

// Handlers holds dependencies for user-facing HTTP handlers.
type Handlers struct {
	DB                *database.DB
	Auth              *auth.Service
	Chat              *chat.Service
	MCPChat           *chat.MCPClient
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

	h.streamSSE(w, r, conv.ID, anthropicMsgs, tools, verbosity)
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
const maxToolRounds = 20

// sseDeadline is the max duration for a single SSE connection. If the tool loop
// is still running when we approach this limit, we pause and let the frontend
// auto-continue with a new request. Set conservatively under typical proxy timeouts.
const sseDeadline = 90 * time.Second

func (h *Handlers) streamSSE(w http.ResponseWriter, r *http.Request, convID int64, msgs []chat.AnthropicMessage, tools []chat.AnthropicToolDef, verbosity string) {
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
		msgs = h.trimContext(msgs)

		eventCh := make(chan chat.SSEEvent, 100)
		var result *chat.StreamResult
		var streamErr error

		go func() {
			result, streamErr = h.Chat.StreamResponse(r.Context(), msgs, tools, verbosity, eventCh)
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

		// If Claude didn't request tool use, we're done.
		if result.StopReason != "tool_use" || len(result.ToolUses) == 0 {
			break
		}

		// Build the assistant message with text + tool_use content blocks.
		assistantContent := []interface{}{}
		if result.Text != "" {
			assistantContent = append(assistantContent, map[string]string{
				"type": "text",
				"text": result.Text,
			})
		}
		for _, tu := range result.ToolUses {
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
		for _, tu := range result.ToolUses {
			var args map[string]interface{}
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				args = map[string]interface{}{}
			}

			h.Log.Info("executing MCP tool", "tool", tu.Name, "conversation_id", convID)
			toolResult, err := h.MCPChat.CallTool(r.Context(), tu.Name, args)

			status := "done"
			if err != nil {
				status = "failed"
				h.Log.Error("tool execution failed", "tool", tu.Name, "error", err)
				toolResult = fmt.Sprintf("Error: %s", err.Error())
			}

			sendEvent(chat.SSEEvent{
				Type:   "tool_done",
				Name:   tu.Name,
				Status: status,
			})

			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": tu.ID,
				"content":     toolResult,
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
