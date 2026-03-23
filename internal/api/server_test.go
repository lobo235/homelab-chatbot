package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	authpkg "github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/database"
)

func testServer(t *testing.T) (*http.ServeMux, *database.DB, *authpkg.Service) {
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
		DB:      db,
		Auth:    authSvc,
		Log:     log,
		Version: "test",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("POST /api/auth/login", h.HandleLogin)
	mux.Handle("POST /api/auth/logout", authSvc.RequireSession(http.HandlerFunc(h.HandleLogout)))
	mux.Handle("GET /api/auth/me", authSvc.RequireSession(http.HandlerFunc(h.HandleGetMe)))
	mux.Handle("GET /api/sessions", authSvc.RequireSession(http.HandlerFunc(h.HandleListSessions)))
	mux.Handle("GET /api/servers", authSvc.RequireSession(http.HandlerFunc(h.HandleListServers)))

	return mux, db, authSvc
}

func loginAsAdmin(t *testing.T, mux *http.ServeMux) *http.Cookie {
	t.Helper()
	body := `{"username":"admin","password":"admin123"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status=%d, body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		if c.Name == authpkg.CookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestHealthEndpoint(t *testing.T) {
	mux, _, _ := testServer(t)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
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

func TestLoginSuccess(t *testing.T) {
	mux, _, _ := testServer(t)
	cookie := loginAsAdmin(t, mux)
	if cookie.Value == "" {
		t.Error("empty session token")
	}
}

func TestLoginBadCredentials(t *testing.T) {
	mux, _, _ := testServer(t)

	body := `{"username":"admin","password":"wrong"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	mux, _, _ := testServer(t)
	cookie := loginAsAdmin(t, mux)

	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status=%d, want 204", rec.Code)
	}
}

func TestGetMe(t *testing.T) {
	mux, _, _ := testServer(t)
	cookie := loginAsAdmin(t, mux)

	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["username"] != "admin" {
		t.Errorf("username=%v", resp["username"])
	}
	if resp["role"] != "admin" {
		t.Errorf("role=%v", resp["role"])
	}
}

func TestListSessions(t *testing.T) {
	mux, _, _ := testServer(t)
	cookie := loginAsAdmin(t, mux)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	var resp []interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	// Should be empty initially.
	if len(resp) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(resp))
	}
}

func TestListServers(t *testing.T) {
	mux, db, _ := testServer(t)
	cookie := loginAsAdmin(t, mux)

	// Add a test server ownership.
	user, _ := db.GetUserByUsername("admin")
	db.CreateServerOwnership("mc-test", user.ID)

	req := httptest.NewRequest("GET", "/api/servers", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	var resp []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp) != 1 {
		t.Fatalf("expected 1 server, got %d", len(resp))
	}
	if resp[0]["name"] != "mc-test" {
		t.Errorf("name=%v", resp[0]["name"])
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	mux, _, _ := testServer(t)

	// Sessions endpoint should require auth.
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}
