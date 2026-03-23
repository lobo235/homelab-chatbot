// Package auth provides user authentication, session management, and HTTP middleware.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/lobo235/homelab-chatbot/internal/database"
)

const (
	// SessionTTL is the duration a session remains valid after last activity.
	SessionTTL = 7 * 24 * time.Hour
	// BcryptCost is the bcrypt work factor.
	BcryptCost = 12
	// CookieName is the session cookie name.
	CookieName = "session"
)

type contextKey string

const userContextKey contextKey = "user"

// Service provides authentication operations.
type Service struct {
	db  *database.DB
	log *slog.Logger
}

// NewService creates a new auth service.
func NewService(db *database.DB, log *slog.Logger) *Service {
	return &Service{db: db, log: log}
}

// HashPassword hashes a password with bcrypt.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	return string(b), err
}

// CheckPassword compares a password with a bcrypt hash.
func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// BootstrapAdmin ensures an admin user exists. Creates one if not.
func (s *Service) BootstrapAdmin(password string) error {
	_, err := s.db.GetUserByUsername("admin")
	if err == nil {
		return nil // admin already exists
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}

	_, err = s.db.CreateUser("admin", hash, "admin")
	if err != nil {
		return err
	}
	s.log.Info("bootstrap admin user created")
	return nil
}

// ErrInvalidCredentials is returned when login fails due to bad username or password.
var ErrInvalidCredentials = fmt.Errorf("invalid credentials")

// Login validates credentials and creates a session. Returns the raw session token.
func (s *Service) Login(username, password string) (string, *database.User, error) {
	user, err := s.db.GetUserByUsername(username)
	if err != nil {
		return "", nil, ErrInvalidCredentials
	}
	if !user.Active {
		return "", nil, ErrInvalidCredentials
	}
	if !CheckPassword(password, user.PasswordHash) {
		return "", nil, ErrInvalidCredentials
	}

	token, hash, err := database.GenerateSessionToken()
	if err != nil {
		return "", nil, err
	}

	if err := s.db.CreateSession(hash, user.ID, SessionTTL); err != nil {
		return "", nil, err
	}

	return token, user, nil
}

// Logout invalidates a session token.
func (s *Service) Logout(token string) error {
	hash := database.HashSessionToken(token)
	return s.db.DeleteSession(hash)
}

// ValidateSession checks if a token is valid and returns the associated user.
func (s *Service) ValidateSession(token string) (*database.User, error) {
	hash := database.HashSessionToken(token)
	sess, err := s.db.GetSessionByToken(hash)
	if err != nil {
		return nil, err
	}

	// Touch session to extend TTL.
	_ = s.db.TouchSession(hash, SessionTTL)

	return s.db.GetUserByID(sess.UserID)
}

// UserFromContext retrieves the authenticated user from the request context.
func UserFromContext(ctx context.Context) *database.User {
	u, _ := ctx.Value(userContextKey).(*database.User)
	return u
}

// RequireSession is middleware that validates session cookies.
func (s *Service) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			writeUnauthorized(w)
			return
		}

		user, err := s.ValidateSession(cookie.Value)
		if err != nil {
			writeUnauthorized(w)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin is middleware that ensures the user is an admin.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil || user.Role != "admin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    "forbidden",
				"message": "Admin access required",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SetSessionCookie sets the session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionTTL.Seconds()),
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    "unauthorized",
		"message": "Authentication required",
	})
}
