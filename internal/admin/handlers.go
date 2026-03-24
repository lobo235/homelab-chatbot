// Package admin provides admin-only HTTP handlers.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/config"
	"github.com/lobo235/homelab-chatbot/internal/database"
	"github.com/lobo235/homelab-chatbot/internal/gateway"
)

// Handlers holds dependencies for admin HTTP handlers.
type Handlers struct {
	DB       *database.DB
	Log      *slog.Logger
	Gateway  *gateway.Client
	Gateways []config.GatewayConfig
}

// HandleListUsers processes GET /admin/users.
func (h *Handlers) HandleListUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := h.DB.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to list users")
		return
	}

	type userItem struct {
		ID            int64  `json:"id"`
		Username      string `json:"username"`
		Role          string `json:"role"`
		VerbosityMode string `json:"verbosity_mode"`
		Active        bool   `json:"active"`
		MaxServers    int    `json:"max_servers"`
		MaxTokens     int    `json:"max_tokens"`
		CreatedAt     string `json:"created_at"`
	}

	items := make([]userItem, 0, len(users))
	for _, u := range users {
		items = append(items, userItem{
			ID:            u.ID,
			Username:      u.Username,
			Role:          u.Role,
			VerbosityMode: u.VerbosityMode,
			Active:        u.Active,
			MaxServers:    u.MaxServers,
			MaxTokens:     u.MaxTokens,
			CreatedAt:     u.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// HandleCreateUser processes POST /admin/users.
func (h *Handlers) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "Username and password are required")
		return
	}
	if body.Role == "" {
		body.Role = "user"
	}
	if body.Role != "user" && body.Role != "admin" {
		writeError(w, http.StatusBadRequest, "invalid_body", "Role must be 'user' or 'admin'")
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to hash password")
		return
	}

	user, err := h.DB.CreateUser(body.Username, hash, body.Role)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", "Username already exists")
		return
	}

	h.Log.Info("user created by admin", "username", body.Username, "role", body.Role)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

// HandleUpdateUser processes PUT /admin/users/{id}.
func (h *Handlers) HandleUpdateUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid user ID")
		return
	}

	var body struct {
		Username      string `json:"username"`
		Password      string `json:"password"`
		Role          string `json:"role"`
		VerbosityMode string `json:"verbosity_mode"`
		Active        *bool  `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}

	var passwordHash string
	if body.Password != "" {
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "Failed to hash password")
			return
		}
		passwordHash = hash
	}

	if err := h.DB.UpdateUser(id, body.Username, passwordHash, body.Role, body.VerbosityMode, body.Active); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	h.Log.Info("user updated by admin", "user_id", id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// HandleDeleteUser processes DELETE /admin/users/{id}.
func (h *Handlers) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid user ID")
		return
	}

	if err := h.DB.DeleteUser(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	h.Log.Info("user deleted by admin", "user_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// HandleSetUserLimits processes PUT /admin/users/{id}/limits.
func (h *Handlers) HandleSetUserLimits(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid user ID")
		return
	}

	var body struct {
		MaxServers int `json:"max_servers"`
		MaxTokens  int `json:"max_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}

	if body.MaxServers <= 0 {
		body.MaxServers = 5
	}
	if body.MaxTokens <= 0 {
		body.MaxTokens = 500000
	}

	if err := h.DB.UpdateUserLimits(id, body.MaxServers, body.MaxTokens); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	h.Log.Info("user limits updated", "user_id", id, "max_servers", body.MaxServers, "max_tokens", body.MaxTokens)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"max_servers": body.MaxServers,
		"max_tokens":  body.MaxTokens,
	})
}

// HandleListAllServers processes GET /admin/servers.
func (h *Handlers) HandleListAllServers(w http.ResponseWriter, _ *http.Request) {
	servers, err := h.DB.ListAllServers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to list servers")
		return
	}

	type serverItem struct {
		Name    string `json:"name"`
		OwnerID int64  `json:"owner_user_id"`
		Owner   string `json:"owner"`
	}

	items := make([]serverItem, 0, len(servers))
	for _, s := range servers {
		owner := ""
		if u, err := h.DB.GetUserByID(s.OwnerID); err == nil {
			owner = u.Username
		}
		items = append(items, serverItem{
			Name:    s.ServerName,
			OwnerID: s.OwnerID,
			Owner:   owner,
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// HandleUsage processes GET /admin/usage.
func (h *Handlers) HandleUsage(w http.ResponseWriter, _ *http.Request) {
	usage, err := h.DB.GetTokenUsage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Failed to get usage")
		return
	}

	writeJSON(w, http.StatusOK, usage)
}

// HandleLogs processes GET /admin/logs — returns recent error information.
func (h *Handlers) HandleLogs(w http.ResponseWriter, _ *http.Request) {
	// In production, this would read from a ring buffer or log file.
	// For now, return a placeholder.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": []interface{}{},
		"note":    "Log aggregation not yet implemented — check container stderr",
	})
}

// HandleStopServer processes POST /admin/servers/{name}/stop.
func (h *Handlers) HandleStopServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "Server name is required")
		return
	}

	var nomadGw *config.GatewayConfig
	for i := range h.Gateways {
		if h.Gateways[i].Name == "nomad" {
			nomadGw = &h.Gateways[i]
			break
		}
	}
	if nomadGw == nil {
		writeError(w, http.StatusServiceUnavailable, "no_gateway", "Nomad gateway not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := h.Gateway.StopNomadJob(ctx, *nomadGw, name); err != nil {
		h.Log.Error("failed to stop server", "name", name, "error", err)
		writeError(w, http.StatusBadGateway, "stop_failed", "Failed to stop server: "+err.Error())
		return
	}

	h.Log.Info("server stopped by admin", "name", name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped", "name": name})
}

// HandleGateways processes GET /admin/gateways.
func (h *Handlers) HandleGateways(w http.ResponseWriter, r *http.Request) {
	if len(h.Gateways) == 0 {
		writeJSON(w, http.StatusOK, []gateway.HealthResult{})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	results := make([]gateway.HealthResult, len(h.Gateways))
	var wg sync.WaitGroup
	for i, gw := range h.Gateways {
		wg.Add(1)
		go func(idx int, g config.GatewayConfig) {
			defer wg.Done()
			results[idx] = h.Gateway.CheckHealth(ctx, g)
		}(i, gw)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, results)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
