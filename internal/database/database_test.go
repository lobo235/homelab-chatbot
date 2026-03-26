package database

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := Open(":memory:", log)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUserCRUD(t *testing.T) {
	db := testDB(t)

	u, err := db.CreateUser("alice", "hash123", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if u.Username != "alice" || u.Role != "user" || u.VerbosityMode != "kid" {
		t.Errorf("unexpected user: %+v", u)
	}

	u2, err := db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("get by username: %v", err)
	}
	if u2.ID != u.ID {
		t.Error("user ID mismatch")
	}

	users, err := db.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("got %d users, want 1", len(users))
	}

	active := false
	if err := db.UpdateUser(u.ID, "", "", "", "", &active); err != nil {
		t.Fatalf("update user: %v", err)
	}
	u3, _ := db.GetUserByID(u.ID)
	if u3.Active {
		t.Error("user should be inactive")
	}

	if err := db.DeleteUser(u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	_, err = db.GetUserByID(u.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestAdminVerbosityDefault(t *testing.T) {
	db := testDB(t)
	u, err := db.CreateUser("admin", "hash", "admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if u.VerbosityMode != "operator" {
		t.Errorf("admin verbosity=%q, want operator", u.VerbosityMode)
	}
}

func TestSessionLifecycle(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("bob", "hash", "user")

	token, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if len(token) != 64 { // 32 bytes hex
		t.Errorf("token length=%d, want 64", len(token))
	}

	if err := db.CreateSession(hash, u.ID, 7*24*time.Hour); err != nil {
		t.Fatalf("create session: %v", err)
	}

	s, err := db.GetSessionByToken(hash)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if s.UserID != u.ID {
		t.Error("session user mismatch")
	}

	// Verify hash function consistency.
	if HashSessionToken(token) != hash {
		t.Error("hash mismatch")
	}

	// Delete session.
	if err := db.DeleteSession(hash); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	_, err = db.GetSessionByToken(hash)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestConversationAndMessages(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("charlie", "hash", "user")

	conv, err := db.CreateConversation(u.ID, "Test chat")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	msg, err := db.AddMessage(conv.ID, "user", "Hello!")
	if err != nil {
		t.Fatalf("add message: %v", err)
	}
	if msg.Role != "user" || msg.Content != "Hello!" {
		t.Errorf("unexpected message: %+v", msg)
	}

	msgs, err := db.GetMessages(conv.ID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("got %d messages, want 1", len(msgs))
	}

	convs, err := db.ListConversations(u.ID)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(convs) != 1 {
		t.Errorf("got %d conversations, want 1", len(convs))
	}

	if err := db.UpdateConversationTokens(conv.ID, 1000); err != nil {
		t.Fatalf("update tokens: %v", err)
	}
	conv2, _ := db.GetConversation(conv.ID)
	if conv2.InputTokens != 1000 {
		t.Errorf("tokens=%d, want 1000", conv2.InputTokens)
	}

	if err := db.DeleteConversation(conv.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
}

func TestServerOwnership(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("dave", "hash", "user")

	if err := db.CreateServerOwnership("mc-test", u.ID); err != nil {
		t.Fatalf("create ownership: %v", err)
	}

	ownerID, err := db.GetServerOwner("mc-test")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	if ownerID != u.ID {
		t.Error("owner mismatch")
	}

	count, err := db.CountServersByOwner(u.ID)
	if err != nil {
		t.Fatalf("count servers: %v", err)
	}
	if count != 1 {
		t.Errorf("count=%d, want 1", count)
	}

	servers, err := db.ListServersByOwner(u.ID)
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("got %d servers, want 1", len(servers))
	}

	if err := db.DeleteServerOwnership("mc-test"); err != nil {
		t.Fatalf("delete ownership: %v", err)
	}

	ownerID, _ = db.GetServerOwner("mc-test")
	if ownerID != 0 {
		t.Error("expected no owner after delete")
	}
}

func TestTokenUsage(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("eve", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Test")
	_ = db.UpdateConversationTokens(conv.ID, 5000)

	usage, err := db.GetTokenUsage()
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if len(usage) != 1 || usage[0].TotalTokens != 5000 {
		t.Errorf("unexpected usage: %+v", usage)
	}
}

func TestUserLimits(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("frank", "hash", "user")

	if err := db.UpdateUserLimits(u.ID, 10, 500000); err != nil {
		t.Fatalf("update limits: %v", err)
	}

	u2, _ := db.GetUserByID(u.ID)
	if u2.MaxServers != 10 || u2.MaxTokens != 500000 {
		t.Errorf("limits: servers=%d tokens=%d", u2.MaxServers, u2.MaxTokens)
	}
}

func TestAsyncOperations(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("grace", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Test async ops")

	// Create an async operation.
	if err := db.CreateAsyncOp(conv.ID, u.ID, "download_to_server", "dl-123", "mc-survival", "downloading mod"); err != nil {
		t.Fatalf("create async op: %v", err)
	}

	// List pending ops.
	ops, err := db.ListPendingOps(conv.ID)
	if err != nil {
		t.Fatalf("list pending ops: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].OperationID != "dl-123" || ops[0].ServerName != "mc-survival" || ops[0].Status != "pending" {
		t.Errorf("unexpected op: %+v", ops[0])
	}

	// Update to done.
	if err := db.UpdateAsyncOpStatus("dl-123", "done"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	// Should no longer appear in pending.
	ops2, _ := db.ListPendingOps(conv.ID)
	if len(ops2) != 0 {
		t.Errorf("got %d pending ops after done, want 0", len(ops2))
	}

	// Create another and test CleanOldOps (all ops are recent, so none cleaned).
	if err := db.CreateAsyncOp(conv.ID, u.ID, "create_backup", "bk-456", "mc-creative", "creating backup"); err != nil {
		t.Fatalf("create backup op: %v", err)
	}
	if err := db.CleanOldOps(); err != nil {
		t.Fatalf("clean old ops: %v", err)
	}
	ops3, _ := db.ListPendingOps(conv.ID)
	if len(ops3) != 1 {
		t.Errorf("got %d pending ops after clean, want 1", len(ops3))
	}
}

func TestAsyncOpsConversationIsolation(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("henry", "hash", "user")
	conv1, _ := db.CreateConversation(u.ID, "Conv 1")
	conv2, _ := db.CreateConversation(u.ID, "Conv 2")

	_ = db.CreateAsyncOp(conv1.ID, u.ID, "download_to_server", "dl-aaa", "mc-test1", "download 1")
	_ = db.CreateAsyncOp(conv2.ID, u.ID, "download_to_server", "dl-bbb", "mc-test2", "download 2")

	ops1, _ := db.ListPendingOps(conv1.ID)
	ops2, _ := db.ListPendingOps(conv2.ID)

	if len(ops1) != 1 || ops1[0].OperationID != "dl-aaa" {
		t.Errorf("conv1 ops: %+v", ops1)
	}
	if len(ops2) != 1 || ops2[0].OperationID != "dl-bbb" {
		t.Errorf("conv2 ops: %+v", ops2)
	}
}

func TestAsyncOpsCascadeDelete(t *testing.T) {
	db := testDB(t)

	u, _ := db.CreateUser("iris", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Test cascade")

	_ = db.CreateAsyncOp(conv.ID, u.ID, "download_to_server", "dl-cascade", "mc-test", "test")

	// Delete the conversation — async ops should cascade.
	if err := db.DeleteConversation(conv.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}

	ops, _ := db.ListPendingOps(conv.ID)
	if len(ops) != 0 {
		t.Errorf("expected 0 ops after cascade delete, got %d", len(ops))
	}
}
