package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	authpkg "github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/chat"
	"github.com/lobo235/homelab-chatbot/internal/database"
)

// testSetup creates a test mux with all the handlers wired up, including
// session/delete routes that need path parameters.
func testSetup(t *testing.T) (*http.ServeMux, *database.DB, *authpkg.Service, *Handlers) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := database.Open(":memory:", log)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	authSvc := authpkg.NewService(db, log)
	if err := authSvc.BootstrapAdmin("admin123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	h := &Handlers{
		DB:                db,
		Auth:              authSvc,
		Log:               log,
		Version:           "test",
		ContextWindowSize: 20,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("POST /api/auth/login", h.HandleLogin)
	mux.Handle("POST /api/auth/logout", authSvc.RequireSession(http.HandlerFunc(h.HandleLogout)))
	mux.Handle("GET /api/auth/me", authSvc.RequireSession(http.HandlerFunc(h.HandleGetMe)))
	mux.Handle("GET /api/sessions", authSvc.RequireSession(http.HandlerFunc(h.HandleListSessions)))
	mux.Handle("GET /api/sessions/{id}", authSvc.RequireSession(http.HandlerFunc(h.HandleGetSession)))
	mux.Handle("DELETE /api/sessions/{id}", authSvc.RequireSession(http.HandlerFunc(h.HandleDeleteSession)))
	mux.Handle("GET /api/servers", authSvc.RequireSession(http.HandlerFunc(h.HandleListServers)))

	return mux, db, authSvc, h
}

func doLogin(t *testing.T, mux *http.ServeMux) *http.Cookie {
	t.Helper()
	body := `{"username":"admin","password":"admin123"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status=%d, body=%s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == authpkg.CookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestHandleLogin_InvalidBody(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleLogin_MissingFields(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	body := `{"username":"","password":""}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleLogin_BadPassword(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	body := `{"username":"admin","password":"wrong"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}

func TestHandleLogin_NonexistentUser(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	body := `{"username":"nobody","password":"pass"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}

func TestHandleLogin_ResponseShape(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	body := `{"username":"admin","password":"admin123"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	user, ok := resp["user"].(map[string]interface{})
	if !ok {
		t.Fatal("missing user object in response")
	}
	if user["username"] != "admin" {
		t.Errorf("username=%v", user["username"])
	}
	if user["role"] != "admin" {
		t.Errorf("role=%v", user["role"])
	}
	if user["verbosity_mode"] != "operator" {
		t.Errorf("verbosity_mode=%v", user["verbosity_mode"])
	}
}

func TestHandleLogout_NoCookie(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Without a cookie, middleware rejects with 401.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}

func TestHandleGetMe_Unauthenticated(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}

func TestHandleGetMe_NilUser(t *testing.T) {
	// Test the handler directly with no user in context.
	_, _, _, h := testSetup(t)

	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	rec := httptest.NewRecorder()
	h.HandleGetMe(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}

func TestHandleGetMe_ResponseFields(t *testing.T) {
	mux, _, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	// Verify all expected fields are present.
	for _, field := range []string{"id", "username", "role", "verbosity_mode", "max_servers", "max_tokens"} {
		if _, ok := resp[field]; !ok {
			t.Errorf("missing field %q in response", field)
		}
	}
}

func TestHandleGetSession_Success(t *testing.T) {
	mux, db, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	// Create a conversation with messages as admin user.
	admin, _ := db.GetUserByUsername("admin")
	conv, _ := db.CreateConversation(admin.ID, "Test session")
	db.AddMessage(conv.ID, "user", "Hello")
	db.AddMessage(conv.ID, "assistant", "Hi there!")

	req := httptest.NewRequest("GET", "/api/sessions/"+strconv.FormatInt(conv.ID, 10), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["title"] != "Test session" {
		t.Errorf("title=%v", resp["title"])
	}
	msgs, ok := resp["messages"].([]interface{})
	if !ok {
		t.Fatal("missing messages array")
	}
	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}
}

func TestHandleGetSession_NotFound(t *testing.T) {
	mux, _, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	req := httptest.NewRequest("GET", "/api/sessions/99999", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
}

func TestHandleGetSession_InvalidID(t *testing.T) {
	mux, _, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	req := httptest.NewRequest("GET", "/api/sessions/abc", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleGetSession_Forbidden(t *testing.T) {
	mux, db, authSvc, _ := testSetup(t)

	// Create a second user and a conversation belonging to them.
	hash, _ := authpkg.HashPassword("pass123")
	otherUser, _ := db.CreateUser("other", hash, "user")
	conv, _ := db.CreateConversation(otherUser.ID, "Private")

	// Login as a non-admin user (create one first).
	hash2, _ := authpkg.HashPassword("userpass")
	db.CreateUser("regular", hash2, "user")

	// Login as regular user.
	body := `{"username":"regular","password":"userpass"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var userCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == authpkg.CookieName {
			userCookie = c
		}
	}
	if userCookie == nil {
		// If regular user login doesn't work, use admin -- but test the forbidden logic directly.
		t.Skip("could not login as regular user")
	}
	_ = authSvc // used for bootstrap

	req = httptest.NewRequest("GET", "/api/sessions/"+strconv.FormatInt(conv.ID, 10), nil)
	req.AddCookie(userCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", rec.Code)
	}
}

func TestHandleDeleteSession_Success(t *testing.T) {
	mux, db, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	admin, _ := db.GetUserByUsername("admin")
	conv, _ := db.CreateConversation(admin.ID, "Delete me")

	req := httptest.NewRequest("DELETE", "/api/sessions/"+strconv.FormatInt(conv.ID, 10), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204", rec.Code)
	}

	// Verify conversation is gone.
	_, err := db.GetConversation(conv.ID)
	if err == nil {
		t.Error("conversation should be deleted")
	}
}

func TestHandleDeleteSession_NotFound(t *testing.T) {
	mux, _, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	req := httptest.NewRequest("DELETE", "/api/sessions/99999", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
}

func TestHandleDeleteSession_InvalidID(t *testing.T) {
	mux, _, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	req := httptest.NewRequest("DELETE", "/api/sessions/abc", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleDeleteSession_Forbidden(t *testing.T) {
	mux, db, _, _ := testSetup(t)

	// Create another user and their conversation.
	hash, _ := authpkg.HashPassword("otherpass")
	otherUser, _ := db.CreateUser("other2", hash, "user")
	conv, _ := db.CreateConversation(otherUser.ID, "Not yours")

	// Create a regular (non-admin) user and login.
	hash2, _ := authpkg.HashPassword("regpass")
	db.CreateUser("regular2", hash2, "user")
	body := `{"username":"regular2","password":"regpass"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var userCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == authpkg.CookieName {
			userCookie = c
		}
	}
	if userCookie == nil {
		t.Skip("could not login as regular2")
	}

	req = httptest.NewRequest("DELETE", "/api/sessions/"+strconv.FormatInt(conv.ID, 10), nil)
	req.AddCookie(userCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", rec.Code)
	}
}

func TestHandleListSessions_WithConversations(t *testing.T) {
	mux, db, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	admin, _ := db.GetUserByUsername("admin")
	conv, _ := db.CreateConversation(admin.ID, "Session 1")
	db.AddMessage(conv.ID, "user", "Hello world")

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	var sessions []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&sessions)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0]["title"] != "Session 1" {
		t.Errorf("title=%v", sessions[0]["title"])
	}
	if sessions[0]["last_message"] != "Hello world" {
		t.Errorf("last_message=%v", sessions[0]["last_message"])
	}
}

func TestHandleListSessions_LastMessageTruncation(t *testing.T) {
	mux, db, _, _ := testSetup(t)
	cookie := doLogin(t, mux)

	admin, _ := db.GetUserByUsername("admin")
	conv, _ := db.CreateConversation(admin.ID, "Long msg")
	// Add a message longer than 100 chars.
	longMsg := strings.Repeat("x", 150)
	db.AddMessage(conv.ID, "user", longMsg)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var sessions []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&sessions)
	lastMsg := sessions[0]["last_message"].(string)
	if len(lastMsg) > 104 { // 100 + "..."
		t.Errorf("last_message not truncated, len=%d", len(lastMsg))
	}
	if !strings.HasSuffix(lastMsg, "...") {
		t.Errorf("expected truncation suffix, got %q", lastMsg[len(lastMsg)-5:])
	}
}

func TestHandleListServers_UserVsAdmin(t *testing.T) {
	mux, db, _, _ := testSetup(t)

	// Create a regular user with a server.
	hash, _ := authpkg.HashPassword("userpass")
	user, _ := db.CreateUser("srvuser", hash, "user")
	db.CreateServerOwnership("mc-user-server", user.ID)

	// Admin also has a server.
	admin, _ := db.GetUserByUsername("admin")
	db.CreateServerOwnership("mc-admin-server", admin.ID)

	// Login as regular user.
	body := `{"username":"srvuser","password":"userpass"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var userCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == authpkg.CookieName {
			userCookie = c
		}
	}

	// Regular user should only see their own server.
	req = httptest.NewRequest("GET", "/api/servers", nil)
	req.AddCookie(userCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var servers []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&servers)
	if len(servers) != 1 {
		t.Errorf("regular user got %d servers, want 1", len(servers))
	}

	// Admin should see all servers.
	adminCookie := doLogin(t, mux)
	req = httptest.NewRequest("GET", "/api/servers", nil)
	req.AddCookie(adminCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	json.NewDecoder(rec.Body).Decode(&servers)
	if len(servers) != 2 {
		t.Errorf("admin got %d servers, want 2", len(servers))
	}
}

func TestResolveConversation_NewConversation(t *testing.T) {
	_, db, _, h := testSetup(t)

	admin, _ := db.GetUserByUsername("admin")
	rec := httptest.NewRecorder()
	req := &chat.Request{Message: "Hello world, this is a test message"}

	conv, err := h.resolveConversation(rec, admin, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if conv == nil {
		t.Fatal("expected non-nil conversation")
	}
	if conv.Title != "Hello world, this is a test message" {
		t.Errorf("title=%q", conv.Title)
	}
}

func TestResolveConversation_TitleTruncation(t *testing.T) {
	_, db, _, h := testSetup(t)

	admin, _ := db.GetUserByUsername("admin")
	rec := httptest.NewRecorder()
	longMsg := strings.Repeat("a", 100)
	req := &chat.Request{Message: longMsg}

	conv, err := h.resolveConversation(rec, admin, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(conv.Title) > 54 { // 50 + "..."
		t.Errorf("title not truncated, len=%d", len(conv.Title))
	}
}

func TestResolveConversation_ExistingConversation(t *testing.T) {
	_, db, _, h := testSetup(t)

	admin, _ := db.GetUserByUsername("admin")
	conv, _ := db.CreateConversation(admin.ID, "Existing")

	rec := httptest.NewRecorder()
	req := &chat.Request{ConversationID: conv.ID, Message: "follow up"}

	resolved, err := h.resolveConversation(rec, admin, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ID != conv.ID {
		t.Errorf("got conv ID %d, want %d", resolved.ID, conv.ID)
	}
}

func TestResolveConversation_NotFound(t *testing.T) {
	_, db, _, h := testSetup(t)

	admin, _ := db.GetUserByUsername("admin")
	rec := httptest.NewRecorder()
	req := &chat.Request{ConversationID: 99999, Message: "test"}

	conv, err := h.resolveConversation(rec, admin, req)
	if err == nil {
		t.Error("expected error for nonexistent conversation")
	}
	if conv != nil {
		t.Error("expected nil conversation")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
}

func TestResolveConversation_Forbidden(t *testing.T) {
	_, db, _, h := testSetup(t)

	admin, _ := db.GetUserByUsername("admin")
	hash, _ := authpkg.HashPassword("pass")
	otherUser, _ := db.CreateUser("stranger", hash, "user")
	conv, _ := db.CreateConversation(otherUser.ID, "Not yours")

	rec := httptest.NewRecorder()
	// Try to access other user's conversation as a different user
	// (we use a fake "user" with a different ID).
	fakeUser := &database.User{ID: 999, Username: "fake", Role: "user"}
	req := &chat.Request{ConversationID: conv.ID, Message: "test"}

	resolved, err := h.resolveConversation(rec, fakeUser, req)
	if err == nil {
		t.Error("expected error for forbidden access")
	}
	if resolved != nil {
		t.Error("expected nil conversation")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", rec.Code)
	}
	_ = admin
}

func TestTrimContext_NoTrimNeeded(t *testing.T) {
	_, _, _, h := testSetup(t)
	h.ContextWindowSize = 10

	msgs := make([]chat.AnthropicMessage, 5)
	for i := range msgs {
		msgs[i] = chat.AnthropicMessage{Role: "user", Content: "msg"}
	}

	result := h.trimContext(msgs)
	if len(result) != 5 {
		t.Errorf("got %d messages, want 5 (no trim needed)", len(result))
	}
}

func TestTrimContext_TrimsMiddle(t *testing.T) {
	_, _, _, h := testSetup(t)
	h.ContextWindowSize = 3

	msgs := make([]chat.AnthropicMessage, 10)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = chat.AnthropicMessage{Role: role, Content: "msg" + strconv.Itoa(i)}
	}

	result := h.trimContext(msgs)
	// Should keep first message + last 3 messages = 4.
	if len(result) != 4 {
		t.Errorf("got %d messages, want 4", len(result))
	}
	// First message should be the original first.
	if result[0].Content != "msg0" {
		t.Errorf("first msg=%v", result[0].Content)
	}
}

func TestTrimContext_ZeroWindowSize(t *testing.T) {
	_, _, _, h := testSetup(t)
	h.ContextWindowSize = 0 // should default to 20

	msgs := make([]chat.AnthropicMessage, 25)
	for i := range msgs {
		msgs[i] = chat.AnthropicMessage{Role: "user", Content: "msg"}
	}

	result := h.trimContext(msgs)
	// Default 20 window: first msg + last 20 = 21.
	if len(result) != 21 {
		t.Errorf("got %d messages, want 21", len(result))
	}
}

func TestIsToolResultMessage(t *testing.T) {
	// Positive case.
	msg := chat.AnthropicMessage{
		Role: "user",
		Content: []interface{}{
			map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": "123",
				"content":     "result",
			},
		},
	}
	if !isToolResultMessage(msg) {
		t.Error("expected tool result message to be detected")
	}

	// Negative case: string content.
	msg2 := chat.AnthropicMessage{Role: "user", Content: "plain text"}
	if isToolResultMessage(msg2) {
		t.Error("plain text should not be tool result")
	}

	// Negative case: array without tool_result.
	msg3 := chat.AnthropicMessage{
		Role: "assistant",
		Content: []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "hello",
			},
		},
	}
	if isToolResultMessage(msg3) {
		t.Error("text block should not be tool result")
	}
}

func TestHandleChat_Unauthenticated(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	// Chat is registered on the full server, but our test mux doesn't have it.
	// Test that HandleChat itself rejects when no user in context.
	_, _, _, h := testSetup(t)

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleChat(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
	_ = mux
}

func TestHandleChat_InvalidBody(t *testing.T) {
	mux, _, _, h := testSetup(t)
	cookie := doLogin(t, mux)

	// Register the chat handler on our test mux with auth middleware.
	authMux := http.NewServeMux()
	authMux.Handle("POST /api/chat", h.Auth.RequireSession(http.HandlerFunc(h.HandleChat)))

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader("not json"))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleChat_EmptyMessage(t *testing.T) {
	mux, _, _, h := testSetup(t)
	cookie := doLogin(t, mux)

	authMux := http.NewServeMux()
	authMux.Handle("POST /api/chat", h.Auth.RequireSession(http.HandlerFunc(h.HandleChat)))

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"message":""}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleHealth_Response(t *testing.T) {
	mux, _, _, _ := testSetup(t)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("content-type=%q", ct)
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status=%q", resp["status"])
	}
	if resp["version"] != "test" {
		t.Errorf("version=%q", resp["version"])
	}
}
