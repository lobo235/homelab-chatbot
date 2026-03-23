package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/chat"
	"github.com/lobo235/homelab-chatbot/internal/database"
)

// Handlers holds dependencies for user-facing HTTP handlers.
type Handlers struct {
	DB      *database.DB
	Auth    *auth.Service
	Chat    *chat.Service
	MCPChat *chat.MCPClient
	Log     *slog.Logger
	Version string
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
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "Message is required")
		return
	}

	conv, err := h.resolveConversation(user, &req)
	if err != nil {
		return // error already written by resolveConversation
	}

	if conv.InputTokens >= int64(user.MaxTokens) {
		writeError(w, http.StatusTooManyRequests, "token_limit",
			fmt.Sprintf("Token limit reached (%d/%d). Start a new conversation.", conv.InputTokens, user.MaxTokens))
		return
	}

	if _, err := h.DB.AddMessage(conv.ID, "user", req.Message); err != nil {
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
func (h *Handlers) resolveConversation(user *database.User, req *chat.Request) (*database.Conversation, error) {
	if req.ConversationID > 0 {
		conv, err := h.DB.GetConversation(req.ConversationID)
		if err != nil {
			return nil, err
		}
		if conv.UserID != user.ID {
			return nil, fmt.Errorf("forbidden")
		}
		return conv, nil
	}

	title := req.Message
	if len(title) > 50 {
		title = title[:50] + "..."
	}
	return h.DB.CreateConversation(user.ID, title)
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

	eventCh := make(chan chat.SSEEvent, 100)
	var fullText string
	var inputTokens int64
	var streamErr error

	go func() {
		fullText, inputTokens, streamErr = h.Chat.StreamResponse(r.Context(), msgs, tools, verbosity, eventCh)
	}()

	for event := range eventCh {
		data, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	if streamErr != nil {
		h.Log.Error("stream error", "error", streamErr, "conversation_id", convID)
		data, _ := json.Marshal(chat.SSEEvent{Type: "error", Message: "Failed to get response from Claude"})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}

	if fullText != "" {
		if _, err := h.DB.AddMessage(convID, "assistant", fullText); err != nil {
			h.Log.Error("store assistant message", "error", err)
		}
	}
	if inputTokens > 0 {
		if err := h.DB.UpdateConversationTokens(convID, inputTokens); err != nil {
			h.Log.Error("update tokens", "error", err)
		}
	}
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
