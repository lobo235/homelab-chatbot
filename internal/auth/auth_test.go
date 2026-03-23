package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/lobo235/homelab-chatbot/internal/database"
)

func testService(t *testing.T) *Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := database.Open(":memory:", log)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db, log)
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !CheckPassword("secret", hash) {
		t.Error("password should match")
	}
	if CheckPassword("wrong", hash) {
		t.Error("wrong password should not match")
	}
}

func TestBootstrapAdmin(t *testing.T) {
	svc := testService(t)

	if err := svc.BootstrapAdmin("admin123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Second call should be a no-op.
	if err := svc.BootstrapAdmin("admin123"); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
}

func TestLoginLogout(t *testing.T) {
	svc := testService(t)
	_ = svc.BootstrapAdmin("admin123")

	token, user, err := svc.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("username=%q", user.Username)
	}
	if token == "" {
		t.Error("empty token")
	}

	// Validate session.
	u, err := svc.ValidateSession(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if u.ID != user.ID {
		t.Error("user mismatch")
	}

	// Logout.
	if err := svc.Logout(token); err != nil {
		t.Fatalf("logout: %v", err)
	}

	// Session should be invalid.
	_, err = svc.ValidateSession(token)
	if err == nil {
		t.Error("expected error after logout")
	}
}

func TestLoginBadPassword(t *testing.T) {
	svc := testService(t)
	_ = svc.BootstrapAdmin("admin123")

	_, _, err := svc.Login("admin", "wrong")
	if err == nil {
		t.Error("expected error for bad password")
	}
}

func TestRequireSessionMiddleware(t *testing.T) {
	svc := testService(t)
	_ = svc.BootstrapAdmin("admin123")

	handler := svc.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			t.Error("no user in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	// No cookie — should fail.
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: status=%d, want 401", rec.Code)
	}

	// With valid session.
	token, _, _ := svc.Login("admin", "admin123")
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("valid session: status=%d, want 200", rec.Code)
	}
}

func TestRequireAdminMiddleware(t *testing.T) {
	svc := testService(t)
	_ = svc.BootstrapAdmin("admin123")

	// Create a regular user.
	hash, _ := HashPassword("pass")
	svc.db.CreateUser("kid", hash, "user")

	handler := svc.RequireSession(RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// Regular user — should get 403.
	token, _, _ := svc.Login("kid", "pass")
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("regular user: status=%d, want 403", rec.Code)
	}

	// Admin user — should get 200.
	token, _, _ = svc.Login("admin", "admin123")
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("admin user: status=%d, want 200", rec.Code)
	}
}
