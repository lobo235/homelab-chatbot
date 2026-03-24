package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/lobo235/homelab-chatbot/internal/config"
	"github.com/lobo235/homelab-chatbot/internal/database"
	"github.com/lobo235/homelab-chatbot/internal/gateway"
)

func testHandlers(t *testing.T) (*Handlers, *database.DB) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := database.Open(":memory:", log)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	h := &Handlers{
		DB:      db,
		Log:     log,
		Gateway: gateway.NewClient(),
	}
	return h, db
}

func TestHandleListUsers(t *testing.T) {
	h, db := testHandlers(t)

	// Create some users.
	db.CreateUser("alice", "hash", "user")
	db.CreateUser("bob", "hash", "admin")

	req := httptest.NewRequest("GET", "/admin/users", nil)
	rec := httptest.NewRecorder()
	h.HandleListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var users []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&users); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("got %d users, want 2", len(users))
	}
}

func TestHandleCreateUser_Success(t *testing.T) {
	h, _ := testHandlers(t)

	body := `{"username":"newuser","password":"pass123","role":"user"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleCreateUser(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["username"] != "newuser" {
		t.Errorf("username=%v", resp["username"])
	}
	if resp["role"] != "user" {
		t.Errorf("role=%v", resp["role"])
	}
}

func TestHandleCreateUser_DefaultRole(t *testing.T) {
	h, _ := testHandlers(t)

	body := `{"username":"norole","password":"pass123"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleCreateUser(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["role"] != "user" {
		t.Errorf("default role=%v, want user", resp["role"])
	}
}

func TestHandleCreateUser_MissingFields(t *testing.T) {
	h, _ := testHandlers(t)

	body := `{"username":"","password":""}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleCreateUser_InvalidRole(t *testing.T) {
	h, _ := testHandlers(t)

	body := `{"username":"badrole","password":"pass123","role":"superadmin"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleCreateUser_InvalidBody(t *testing.T) {
	h, _ := testHandlers(t)

	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleCreateUser_Duplicate(t *testing.T) {
	h, db := testHandlers(t)
	db.CreateUser("existing", "hash", "user")

	body := `{"username":"existing","password":"pass123"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleCreateUser(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status=%d, want 409", rec.Code)
	}
}

func TestHandleUpdateUser_Success(t *testing.T) {
	h, db := testHandlers(t)
	u, _ := db.CreateUser("upd", "hash", "user")

	body := `{"role":"admin","verbosity_mode":"operator"}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}", h.HandleUpdateUser)

	req := httptest.NewRequest("PUT", "/admin/users/"+itoa(u.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	updated, _ := db.GetUserByID(u.ID)
	if updated.Role != "admin" {
		t.Errorf("role=%q, want admin", updated.Role)
	}
}

func TestHandleUpdateUser_InvalidID(t *testing.T) {
	h, _ := testHandlers(t)

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}", h.HandleUpdateUser)

	req := httptest.NewRequest("PUT", "/admin/users/abc", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleUpdateUser_InvalidBody(t *testing.T) {
	h, db := testHandlers(t)
	u, _ := db.CreateUser("updbad", "hash", "user")

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}", h.HandleUpdateUser)

	req := httptest.NewRequest("PUT", "/admin/users/"+itoa(u.ID), strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleUpdateUser_WithPassword(t *testing.T) {
	h, db := testHandlers(t)
	u, _ := db.CreateUser("passupd", "hash", "user")

	body := `{"password":"newpass123"}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}", h.HandleUpdateUser)

	req := httptest.NewRequest("PUT", "/admin/users/"+itoa(u.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	updated, _ := db.GetUserByID(u.ID)
	if updated.PasswordHash == "hash" {
		t.Error("password hash should have changed")
	}
}

func TestHandleDeleteUser(t *testing.T) {
	h, db := testHandlers(t)
	u, _ := db.CreateUser("deleteme", "hash", "user")

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /admin/users/{id}", h.HandleDeleteUser)

	req := httptest.NewRequest("DELETE", "/admin/users/"+itoa(u.ID), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204", rec.Code)
	}

	_, err := db.GetUserByID(u.ID)
	if err == nil {
		t.Error("user should be deleted")
	}
}

func TestHandleDeleteUser_InvalidID(t *testing.T) {
	h, _ := testHandlers(t)

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /admin/users/{id}", h.HandleDeleteUser)

	req := httptest.NewRequest("DELETE", "/admin/users/abc", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleSetUserLimits_Success(t *testing.T) {
	h, db := testHandlers(t)
	u, _ := db.CreateUser("limituser", "hash", "user")

	body := `{"max_servers":10,"max_tokens":1000000}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}/limits", h.HandleSetUserLimits)

	req := httptest.NewRequest("PUT", "/admin/users/"+itoa(u.ID)+"/limits", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	updated, _ := db.GetUserByID(u.ID)
	if updated.MaxServers != 10 {
		t.Errorf("max_servers=%d, want 10", updated.MaxServers)
	}
	if updated.MaxTokens != 1000000 {
		t.Errorf("max_tokens=%d, want 1000000", updated.MaxTokens)
	}
}

func TestHandleSetUserLimits_Defaults(t *testing.T) {
	h, db := testHandlers(t)
	u, _ := db.CreateUser("deflimits", "hash", "user")

	// Zero/negative values should default.
	body := `{"max_servers":0,"max_tokens":-1}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}/limits", h.HandleSetUserLimits)

	req := httptest.NewRequest("PUT", "/admin/users/"+itoa(u.ID)+"/limits", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["max_servers"] != float64(5) {
		t.Errorf("max_servers=%v, want 5", resp["max_servers"])
	}
	if resp["max_tokens"] != float64(500000) {
		t.Errorf("max_tokens=%v, want 500000", resp["max_tokens"])
	}
}

func TestHandleSetUserLimits_InvalidID(t *testing.T) {
	h, _ := testHandlers(t)

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}/limits", h.HandleSetUserLimits)

	req := httptest.NewRequest("PUT", "/admin/users/abc/limits", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleSetUserLimits_InvalidBody(t *testing.T) {
	h, db := testHandlers(t)
	u, _ := db.CreateUser("badbodylimit", "hash", "user")

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /admin/users/{id}/limits", h.HandleSetUserLimits)

	req := httptest.NewRequest("PUT", "/admin/users/"+itoa(u.ID)+"/limits", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleListAllServers(t *testing.T) {
	h, db := testHandlers(t)

	u, _ := db.CreateUser("srvowner", "hash", "user")
	db.CreateServerOwnership("mc-one", u.ID)
	db.CreateServerOwnership("mc-two", u.ID)

	req := httptest.NewRequest("GET", "/admin/servers", nil)
	rec := httptest.NewRecorder()
	h.HandleListAllServers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var servers []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&servers)
	if len(servers) != 2 {
		t.Errorf("got %d servers, want 2", len(servers))
	}
	if servers[0]["owner"] != "srvowner" {
		t.Errorf("owner=%v, want srvowner", servers[0]["owner"])
	}
}

func TestHandleListAllServers_Empty(t *testing.T) {
	h, _ := testHandlers(t)

	req := httptest.NewRequest("GET", "/admin/servers", nil)
	rec := httptest.NewRecorder()
	h.HandleListAllServers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var servers []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&servers)
	if len(servers) != 0 {
		t.Errorf("got %d servers, want 0", len(servers))
	}
}

func TestHandleUsage(t *testing.T) {
	h, db := testHandlers(t)

	u, _ := db.CreateUser("usageuser", "hash", "user")
	conv, _ := db.CreateConversation(u.ID, "Test")
	db.UpdateConversationTokens(conv.ID, 10000)

	req := httptest.NewRequest("GET", "/admin/usage", nil)
	rec := httptest.NewRecorder()
	h.HandleUsage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var usage []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&usage)
	if len(usage) != 1 {
		t.Fatalf("got %d usage records, want 1", len(usage))
	}
	if usage[0]["total_tokens"] != float64(10000) {
		t.Errorf("total_tokens=%v, want 10000", usage[0]["total_tokens"])
	}
}

func TestHandleLogs(t *testing.T) {
	h, _ := testHandlers(t)

	req := httptest.NewRequest("GET", "/admin/logs", nil)
	rec := httptest.NewRecorder()
	h.HandleLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	entries, ok := resp["entries"].([]interface{})
	if !ok {
		t.Fatal("expected entries array")
	}
	if len(entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(entries))
	}
}

func TestHandleGateways_NoGateways(t *testing.T) {
	h, _ := testHandlers(t)
	h.Gateways = nil

	req := httptest.NewRequest("GET", "/admin/gateways", nil)
	rec := httptest.NewRecorder()
	h.HandleGateways(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var results []gateway.HealthResult
	json.NewDecoder(rec.Body).Decode(&results)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestHandleGateways_WithMockGateway(t *testing.T) {
	// Start a mock gateway server that returns 200 on /health.
	mockGw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer mockGw.Close()

	h, _ := testHandlers(t)
	h.Gateways = []config.GatewayConfig{
		{Name: "test-gw", URL: mockGw.URL, Key: "test-key"},
	}

	req := httptest.NewRequest("GET", "/admin/gateways", nil)
	rec := httptest.NewRecorder()
	h.HandleGateways(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}

	var results []gateway.HealthResult
	json.NewDecoder(rec.Body).Decode(&results)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Healthy {
		t.Errorf("expected healthy=true, got error=%q", results[0].Error)
	}
}

func TestHandleStopServer_NoNomadGateway(t *testing.T) {
	h, _ := testHandlers(t)
	h.Gateways = nil

	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/servers/{name}/stop", h.HandleStopServer)

	req := httptest.NewRequest("POST", "/admin/servers/mc-test/stop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", rec.Code)
	}
}

func TestHandleStopServer_EmptyName(t *testing.T) {
	h, _ := testHandlers(t)

	// Simulate empty name by calling handler directly with no path value.
	req := httptest.NewRequest("POST", "/admin/servers//stop", nil)
	rec := httptest.NewRecorder()
	h.HandleStopServer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleStopServer_Success(t *testing.T) {
	// Mock a nomad gateway that accepts DELETE /jobs/{name}.
	mockNomad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/jobs/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockNomad.Close()

	h, _ := testHandlers(t)
	h.Gateways = []config.GatewayConfig{
		{Name: "nomad", URL: mockNomad.URL, Key: "test-key"},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/servers/{name}/stop", h.HandleStopServer)

	req := httptest.NewRequest("POST", "/admin/servers/mc-test/stop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["status"] != "stopped" {
		t.Errorf("status=%q, want stopped", resp["status"])
	}
}

func TestHandleStopServer_GatewayError(t *testing.T) {
	// Mock a nomad gateway that returns 500.
	mockNomad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockNomad.Close()

	h, _ := testHandlers(t)
	h.Gateways = []config.GatewayConfig{
		{Name: "nomad", URL: mockNomad.URL, Key: ""},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/servers/{name}/stop", h.HandleStopServer)

	req := httptest.NewRequest("POST", "/admin/servers/mc-fail/stop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status=%d, want 502", rec.Code)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
