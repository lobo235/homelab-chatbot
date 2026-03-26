// Package notify provides async operation notifications via per-user SSE streams.
package notify

import (
	"log/slog"
	"sync"
)

// Event is a notification sent to the frontend via SSE.
type Event struct {
	Type        string `json:"type"`                      // async_started, async_progress, async_complete, async_failed, auto_continue
	OpID        string `json:"op_id"`                     // operation ID
	OpType      string `json:"op_type,omitempty"`         // download, backup
	ServerName  string `json:"server_name,omitempty"`     // mc-{name}
	Description string `json:"description,omitempty"`     // human-readable description
	Status      string `json:"status,omitempty"`          // pending, done, failed
	Message     string `json:"message,omitempty"`         // completion/error message
	ElapsedSec  int    `json:"elapsed_seconds,omitempty"` // seconds since op started
	ConvID      int64  `json:"conversation_id,omitempty"` // for auto_continue
}

// Client represents a single SSE connection for a user.
type Client struct {
	UserID int64
	Events chan Event
	done   chan struct{}
}

// Done returns a channel that is closed when the client is unregistered.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Hub manages per-user notification SSE client connections.
type Hub struct {
	mu      sync.RWMutex
	clients map[int64][]*Client
	log     *slog.Logger
}

// NewHub creates a new notification hub.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[int64][]*Client),
		log:     log,
	}
}

// Register adds a new SSE client for the given user and returns it.
func (h *Hub) Register(userID int64) *Client {
	c := &Client{
		UserID: userID,
		Events: make(chan Event, 32),
		done:   make(chan struct{}),
	}
	h.mu.Lock()
	h.clients[userID] = append(h.clients[userID], c)
	h.mu.Unlock()
	h.log.Debug("notification client registered", "user_id", userID)
	return c
}

// Unregister removes the client and closes its channels.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	clients := h.clients[c.UserID]
	for i, existing := range clients {
		if existing == c {
			h.clients[c.UserID] = append(clients[:i], clients[i+1:]...)
			break
		}
	}
	if len(h.clients[c.UserID]) == 0 {
		delete(h.clients, c.UserID)
	}

	close(c.done)
	h.log.Debug("notification client unregistered", "user_id", c.UserID)
}

// Send fans out an event to all SSE connections for the given user.
// Non-blocking: drops the event if a client's channel is full.
func (h *Hub) Send(userID int64, event Event) {
	h.mu.RLock()
	clients := h.clients[userID]
	h.mu.RUnlock()

	for _, c := range clients {
		select {
		case c.Events <- event:
		default:
			h.log.Warn("notification dropped, client buffer full", "user_id", userID, "event_type", event.Type)
		}
	}
}

// HasClients returns true if the user has at least one active SSE connection.
func (h *Hub) HasClients(userID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID]) > 0
}

// ClientCount returns the total number of active SSE connections (for debugging).
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for _, clients := range h.clients {
		total += len(clients)
	}
	return total
}
