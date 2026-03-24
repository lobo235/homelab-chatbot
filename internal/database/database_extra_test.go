package database

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListAllServers(t *testing.T) {
	db := testDB(t)

	u1, _ := db.CreateUser("owner1", "hash", "user")
	u2, _ := db.CreateUser("owner2", "hash", "user")

	_ = db.CreateServerOwnership("mc-alpha", u1.ID)
	_ = db.CreateServerOwnership("mc-beta", u2.ID)
	_ = db.CreateServerOwnership("mc-gamma", u1.ID)

	servers, err := db.ListAllServers()
	if err != nil {
		t.Fatalf("list all servers: %v", err)
	}
	if len(servers) != 3 {
		t.Errorf("got %d servers, want 3", len(servers))
	}
}

func TestTouchSession(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("touchuser", "hash", "user")

	_, hash, _ := GenerateSessionToken()
	if err := db.CreateSession(hash, u.ID, 7*24*time.Hour); err != nil {
		t.Fatalf("create session: %v", err)
	}

	s1, err := db.GetSessionByToken(hash)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	origExpiry := s1.ExpiresAt

	// Touch extends the session TTL by resetting the expiry.
	if err := db.TouchSession(hash, 14*24*time.Hour); err != nil {
		t.Fatalf("touch session: %v", err)
	}

	s2, err := db.GetSessionByToken(hash)
	if err != nil {
		t.Fatalf("get session after touch: %v", err)
	}

	if !s2.ExpiresAt.After(origExpiry) {
		t.Errorf("expected extended expiry after touch: before=%v, after=%v", origExpiry, s2.ExpiresAt)
	}
}

func TestCleanExpiredSessions(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("cleanuser", "hash", "user")

	// Create an already-expired session (TTL of 0 means expires immediately).
	_, hash1, _ := GenerateSessionToken()
	// Insert with a past expiration.
	_, err := db.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, datetime('now', '-1 hour'))`,
		hash1, u.ID,
	)
	if err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	// Create a valid session.
	_, hash2, _ := GenerateSessionToken()
	if err := db.CreateSession(hash2, u.ID, 24*time.Hour); err != nil {
		t.Fatalf("create valid session: %v", err)
	}

	if err := db.CleanExpiredSessions(); err != nil {
		t.Fatalf("clean expired: %v", err)
	}

	// Expired session should be gone.
	_, err = db.GetSessionByToken(hash1)
	if err == nil {
		t.Error("expected expired session to be cleaned")
	}

	// Valid session should remain.
	s, err := db.GetSessionByToken(hash2)
	if err != nil {
		t.Fatalf("valid session should remain: %v", err)
	}
	if s == nil {
		t.Error("valid session is nil")
	}
}

func TestGetLastMessage(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("lastmsguser", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Test")

	_, _ = db.AddMessage(conv.ID, "user", "First message")
	_, _ = db.AddMessage(conv.ID, "assistant", "Second message")
	_, _ = db.AddMessage(conv.ID, "user", "Third message")

	last, err := db.GetLastMessage(conv.ID)
	if err != nil {
		t.Fatalf("get last message: %v", err)
	}
	if last.Content != "Third message" {
		t.Errorf("last message=%q, want %q", last.Content, "Third message")
	}
	if last.Role != "user" {
		t.Errorf("last role=%q, want user", last.Role)
	}
}

func TestGetLastMessage_NoMessages(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("emptymsg", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Empty")

	_, err := db.GetLastMessage(conv.ID)
	if err == nil {
		t.Error("expected error for empty conversation")
	}
}

func TestUpdateUser_AllFields(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("updateme", "hash", "user")

	active := true
	if err := db.UpdateUser(u.ID, "newname", "newhash", "admin", "operator", &active); err != nil {
		t.Fatalf("update all fields: %v", err)
	}

	u2, _ := db.GetUserByID(u.ID)
	if u2.Username != "newname" {
		t.Errorf("username=%q, want newname", u2.Username)
	}
	if u2.PasswordHash != "newhash" {
		t.Errorf("password hash not updated")
	}
	if u2.Role != "admin" {
		t.Errorf("role=%q, want admin", u2.Role)
	}
	if u2.VerbosityMode != "operator" {
		t.Errorf("verbosity=%q, want operator", u2.VerbosityMode)
	}
	if !u2.Active {
		t.Error("expected active=true")
	}
}

func TestUpdateUser_NonExistent(t *testing.T) {
	db := testDB(t)
	err := db.UpdateUser(9999, "x", "", "", "", nil)
	if err == nil {
		t.Error("expected error for non-existent user")
	}
}

func TestGetServerOwner_NotFound(t *testing.T) {
	db := testDB(t)
	ownerID, err := db.GetServerOwner("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ownerID != 0 {
		t.Errorf("expected 0 owner, got %d", ownerID)
	}
}

func TestConversationTokenAccumulation(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("tokenuser", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Tokens")

	_ = db.UpdateConversationTokens(conv.ID, 1000)
	_ = db.UpdateConversationTokens(conv.ID, 2000)

	c, _ := db.GetConversation(conv.ID)
	if c.InputTokens != 3000 {
		t.Errorf("tokens=%d, want 3000", c.InputTokens)
	}
}

func TestMultipleConversations(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("multiconv", "hash", "user")

	_, _ = db.CreateConversation(u.ID, "Conv 1")
	_, _ = db.CreateConversation(u.ID, "Conv 2")
	_, _ = db.CreateConversation(u.ID, "Conv 3")

	convs, err := db.ListConversations(u.ID)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(convs) != 3 {
		t.Errorf("got %d conversations, want 3", len(convs))
	}
}

func TestDeleteConversation_Messages(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("delconv", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Delete me")

	_, _ = db.AddMessage(conv.ID, "user", "msg1")
	_, _ = db.AddMessage(conv.ID, "assistant", "msg2")

	if err := db.DeleteConversation(conv.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := db.GetConversation(conv.ID)
	if err == nil {
		t.Error("expected error after deleting conversation")
	}
}

func TestTokenUsage_MultipleUsers(t *testing.T) {
	db := testDB(t)

	u1, _ := db.CreateUser("usage1", "hash", "user")
	u2, _ := db.CreateUser("usage2", "hash", "user")

	c1, _ := db.CreateConversation(u1.ID, "C1")
	c2, _ := db.CreateConversation(u2.ID, "C2")

	_ = db.UpdateConversationTokens(c1.ID, 5000)
	_ = db.UpdateConversationTokens(c2.ID, 3000)

	usage, err := db.GetTokenUsage()
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if len(usage) != 2 {
		t.Fatalf("expected 2 usage records, got %d", len(usage))
	}
	// Results are ordered by total_tokens DESC.
	if usage[0].TotalTokens != 5000 {
		t.Errorf("first user tokens=%d, want 5000", usage[0].TotalTokens)
	}
	if usage[1].TotalTokens != 3000 {
		t.Errorf("second user tokens=%d, want 3000", usage[1].TotalTokens)
	}
}

func TestOpenFileBasedDB(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	db, err := Open(dir, log)
	if err != nil {
		t.Fatalf("open file-based db: %v", err)
	}
	defer db.Close()

	// Verify the DB file was created.
	dbPath := filepath.Join(dir, "chatbot.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("db file not found: %v", err)
	}
	// Check permissions are 0600.
	if info.Mode().Perm() != 0600 {
		t.Errorf("db permissions=%o, want 0600", info.Mode().Perm())
	}

	// Verify it works.
	_, err = db.CreateUser("fileuser", "hash", "user")
	if err != nil {
		t.Fatalf("create user in file db: %v", err)
	}
}

func TestDuplicateUsername(t *testing.T) {
	db := testDB(t)
	_, err := db.CreateUser("dupe", "hash", "user")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = db.CreateUser("dupe", "hash2", "user")
	if err == nil {
		t.Error("expected error for duplicate username")
	}
}

func TestDuplicateServerOwnership(t *testing.T) {
	db := testDB(t)
	u, _ := db.CreateUser("dupeowner", "hash", "user")

	if err := db.CreateServerOwnership("same-server", u.ID); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := db.CreateServerOwnership("same-server", u.ID)
	if err == nil {
		t.Error("expected error for duplicate server ownership")
	}
}
