package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/lobo235/homelab-chatbot/internal/admin"
	"github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/chat"
	"github.com/lobo235/homelab-chatbot/internal/config"
	"github.com/lobo235/homelab-chatbot/internal/database"
	"github.com/lobo235/homelab-chatbot/internal/frontend"
	"github.com/lobo235/homelab-chatbot/internal/gateway"
)

// Server is the HTTP server for the chatbot.
type Server struct {
	httpServer *http.Server
	log        *slog.Logger
}

// NewServer creates a new HTTP server with all routes registered.
func NewServer(db *database.DB, authSvc *auth.Service, chatSvc *chat.Service, mcpClient *chat.MCPClient, gateways []config.GatewayConfig, version string, log *slog.Logger) *Server {
	mux := http.NewServeMux()

	h := &Handlers{
		DB:      db,
		Auth:    authSvc,
		Chat:    chatSvc,
		MCPChat: mcpClient,
		Log:     log,
		Version: version,
	}

	ah := &admin.Handlers{
		DB:       db,
		Log:      log,
		Gateway:  gateway.NewClient(),
		Gateways: gateways,
	}

	// Unauthenticated routes.
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("POST /api/auth/login", h.HandleLogin)
	mux.HandleFunc("GET /help", frontend.HandleHelp)

	// Session-protected routes.
	sessionMw := authSvc.RequireSession
	mux.Handle("POST /api/auth/logout", sessionMw(http.HandlerFunc(h.HandleLogout)))
	mux.Handle("GET /api/auth/me", sessionMw(http.HandlerFunc(h.HandleGetMe)))
	mux.Handle("POST /api/chat", sessionMw(http.HandlerFunc(h.HandleChat)))
	mux.Handle("GET /api/sessions", sessionMw(http.HandlerFunc(h.HandleListSessions)))
	mux.Handle("GET /api/sessions/{id}", sessionMw(http.HandlerFunc(h.HandleGetSession)))
	mux.Handle("DELETE /api/sessions/{id}", sessionMw(http.HandlerFunc(h.HandleDeleteSession)))
	mux.Handle("GET /api/servers", sessionMw(http.HandlerFunc(h.HandleListServers)))

	// Admin-only routes.
	adminMw := func(next http.Handler) http.Handler {
		return sessionMw(auth.RequireAdmin(next))
	}
	mux.Handle("GET /admin/users", adminMw(http.HandlerFunc(ah.HandleListUsers)))
	mux.Handle("POST /admin/users", adminMw(http.HandlerFunc(ah.HandleCreateUser)))
	mux.Handle("PUT /admin/users/{id}", adminMw(http.HandlerFunc(ah.HandleUpdateUser)))
	mux.Handle("DELETE /admin/users/{id}", adminMw(http.HandlerFunc(ah.HandleDeleteUser)))
	mux.Handle("PUT /admin/users/{id}/limits", adminMw(http.HandlerFunc(ah.HandleSetUserLimits)))
	mux.Handle("GET /admin/servers", adminMw(http.HandlerFunc(ah.HandleListAllServers)))
	mux.Handle("POST /admin/servers/{name}/stop", adminMw(http.HandlerFunc(ah.HandleStopServer)))
	mux.Handle("GET /admin/gateways", adminMw(http.HandlerFunc(ah.HandleGateways)))
	mux.Handle("GET /admin/usage", adminMw(http.HandlerFunc(ah.HandleUsage)))
	mux.Handle("GET /admin/logs", adminMw(http.HandlerFunc(ah.HandleLogs)))

	// Frontend (catch-all for SPA).
	mux.HandleFunc("GET /", frontend.HandleIndex)

	// Security headers and request logging middleware wrapping all routes.
	handler := securityHeaders(requestLogger(mux, log))

	return &Server{
		httpServer: &http.Server{
			Handler:      handler,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 120 * time.Second, // Long timeout for SSE streaming.
			IdleTimeout:  60 * time.Second,
		},
		log: log,
	}
}

// Run starts the server and blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	s.httpServer.Addr = addr
	s.log.Info("listening", "addr", addr)

	go func() {
		<-ctx.Done()
		s.log.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
	}()

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func securityHeaders(next http.Handler) http.Handler {
	csp := "default-src 'self'; " +
		"script-src 'self' https://cdn.jsdelivr.net 'unsafe-inline' 'unsafe-eval'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"connect-src 'self'; " +
		"img-src 'self' data:"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration", time.Since(start).String(),
			"remote", r.RemoteAddr,
		)
	})
}
