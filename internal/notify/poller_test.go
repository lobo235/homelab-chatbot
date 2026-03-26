package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lobo235/homelab-chatbot/internal/database"
)

// mockMCPCaller implements MCPCaller for testing.
type mockMCPCaller struct {
	mu        sync.Mutex
	responses map[string]string // tool:opID → JSON response
	calls     []mockCall
}

type mockCall struct {
	Tool string
	Args map[string]interface{}
}

func (m *mockMCPCaller) CallTool(_ context.Context, name string, args map[string]interface{}) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{Tool: name, Args: args})

	var opID string
	if id, ok := args["download_id"]; ok {
		opID = fmt.Sprint(id)
	} else if id, ok := args["backup_id"]; ok {
		opID = fmt.Sprint(id)
	}
	key := name + ":" + opID
	if resp, ok := m.responses[key]; ok {
		return resp, nil
	}
	return `{"status":"running"}`, nil
}

func (m *mockMCPCaller) getCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockCall, len(m.calls))
	copy(cp, m.calls)
	return cp
}

// testFixture holds common test dependencies.
type testFixture struct {
	db     *database.DB
	userID int64
	convID int64
}

// setupTestDB creates an in-memory DB with a test user and conversation.
func setupTestDB(t *testing.T) testFixture {
	t.Helper()
	db, err := database.Open(":memory:", testLogger())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	u, err := db.CreateUser("testuser", "hash", "user")
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	conv, err := db.CreateConversation(u.ID, "test conversation")
	if err != nil {
		t.Fatalf("create test conversation: %v", err)
	}
	return testFixture{db: db, userID: u.ID, convID: conv.ID}
}

func TestPoller_DetectsCompletion(t *testing.T) {
	f := setupTestDB(t)
	hub := NewHub(testLogger())
	client := hub.Register(f.userID)

	mcp := &mockMCPCaller{
		responses: map[string]string{
			"get_download_status:dl-001": `{"status":"done"}`,
		},
	}

	if err := f.db.CreateAsyncOp(f.convID, f.userID, "download_to_server", "dl-001", "mc-test", "Test download"); err != nil {
		t.Fatal(err)
	}

	var completedOp database.AsyncOp
	var completedStatus string

	p := NewPoller(f.db, mcp, hub, testLogger(), time.Hour)
	p.OnComplete = func(op database.AsyncOp, status string) {
		completedOp = op
		completedStatus = status
	}

	p.poll(context.Background())

	select {
	case event := <-client.Events:
		if event.Type != "async_complete" {
			t.Fatalf("expected async_complete, got %s", event.Type)
		}
		if event.OpID != "dl-001" {
			t.Fatalf("expected op_id dl-001, got %s", event.OpID)
		}
		if event.Status != "done" {
			t.Fatalf("expected status done, got %s", event.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}

	if completedStatus != "done" {
		t.Fatalf("expected OnComplete with done, got %q", completedStatus)
	}
	if completedOp.OperationID != "dl-001" {
		t.Fatalf("expected op dl-001, got %s", completedOp.OperationID)
	}

	ops, err := f.db.ListAllPendingOps()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("expected 0 pending ops, got %d", len(ops))
	}
}

func TestPoller_DetectsFailure(t *testing.T) {
	f := setupTestDB(t)
	hub := NewHub(testLogger())
	client := hub.Register(f.userID)

	mcp := &mockMCPCaller{
		responses: map[string]string{
			"get_download_status:dl-002": `{"status":"failed","error":"connection timeout"}`,
		},
	}

	if err := f.db.CreateAsyncOp(f.convID, f.userID, "download_to_server", "dl-002", "mc-test", "Test download"); err != nil {
		t.Fatal(err)
	}

	p := NewPoller(f.db, mcp, hub, testLogger(), time.Hour)
	p.poll(context.Background())

	select {
	case event := <-client.Events:
		if event.Type != "async_failed" {
			t.Fatalf("expected async_failed, got %s", event.Type)
		}
		if event.Status != "failed" {
			t.Fatalf("expected status failed, got %s", event.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestPoller_SendsProgress(t *testing.T) {
	f := setupTestDB(t)
	hub := NewHub(testLogger())
	client := hub.Register(f.userID)

	mcp := &mockMCPCaller{} // default returns {"status":"running"}

	if err := f.db.CreateAsyncOp(f.convID, f.userID, "download_to_server", "dl-003", "mc-test", "Big download"); err != nil {
		t.Fatal(err)
	}

	p := NewPoller(f.db, mcp, hub, testLogger(), time.Hour)
	p.poll(context.Background())

	select {
	case event := <-client.Events:
		if event.Type != "async_progress" {
			t.Fatalf("expected async_progress, got %s", event.Type)
		}
		if event.OpType != "download" {
			t.Fatalf("expected op_type download, got %s", event.OpType)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}

	ops, err := f.db.ListAllPendingOps()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 pending op, got %d", len(ops))
	}
}

func TestPoller_BackupTool(t *testing.T) {
	f := setupTestDB(t)
	hub := NewHub(testLogger())
	hub.Register(f.userID)

	mcp := &mockMCPCaller{
		responses: map[string]string{
			"get_backup_status:bk-001": `{"status":"done"}`,
		},
	}

	if err := f.db.CreateAsyncOp(f.convID, f.userID, "create_backup", "bk-001", "mc-test", "Test backup"); err != nil {
		t.Fatal(err)
	}

	p := NewPoller(f.db, mcp, hub, testLogger(), time.Hour)
	p.poll(context.Background())

	calls := mcp.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Tool != "get_backup_status" {
		t.Fatalf("expected get_backup_status, got %s", calls[0].Tool)
	}
}

func TestPoller_NilMCP(t *testing.T) {
	p := NewPoller(nil, nil, NewHub(testLogger()), testLogger(), time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p.Run(ctx)
}

func TestStatusToolFor(t *testing.T) {
	p := &Poller{log: testLogger()}

	tests := []struct {
		tool     string
		wantTool string
		wantArg  string
	}{
		{"download_to_server", "get_download_status", "download_id"},
		{"create_backup", "get_backup_status", "backup_id"},
		{"unknown_tool", "", ""},
	}

	for _, tt := range tests {
		op := database.AsyncOp{ToolName: tt.tool, ServerName: "mc-test", OperationID: "op-1"}
		name, args := p.statusToolFor(op)
		if name != tt.wantTool {
			t.Errorf("tool %s: got name %q, want %q", tt.tool, name, tt.wantTool)
		}
		if tt.wantArg != "" {
			if _, ok := args[tt.wantArg]; !ok {
				t.Errorf("tool %s: missing arg %q", tt.tool, tt.wantArg)
			}
		}
	}
}

func TestStatusResponseParsing(t *testing.T) {
	raw := `{"id":"2026-03-25T12-30-00","status":"done","url":"https://example.com/pack.zip","dest_path":"/","extract":true,"started_at":"2026-03-25T12:30:00Z","completed_at":"2026-03-25T12:32:15Z","result":{"files_count":142,"total_bytes":1073741824}}`
	var sr statusResponse
	if err := json.Unmarshal([]byte(raw), &sr); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sr.Status != "done" {
		t.Fatalf("expected done, got %s", sr.Status)
	}
}
