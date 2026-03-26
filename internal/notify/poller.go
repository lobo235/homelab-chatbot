package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	"github.com/lobo235/homelab-chatbot/internal/database"
)

// MCPCaller is the interface for calling MCP tools. Satisfied by chat.MCPClient.
type MCPCaller interface {
	CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error)
}

// CompletionHandler is called when an async operation reaches a terminal state.
type CompletionHandler func(op database.AsyncOp, terminalStatus string)

// Poller checks pending async operations and sends notifications when they complete.
type Poller struct {
	db         *database.DB
	mcp        MCPCaller
	hub        *Hub
	log        *slog.Logger
	interval   time.Duration
	OnComplete CompletionHandler
}

// NewPoller creates a new background poller.
func NewPoller(db *database.DB, mcp MCPCaller, hub *Hub, log *slog.Logger, interval time.Duration) *Poller {
	return &Poller{
		db:       db,
		mcp:      mcp,
		hub:      hub,
		log:      log,
		interval: interval,
	}
}

// Run starts the polling loop. It blocks until the context is cancelled.
func (p *Poller) Run(ctx context.Context) {
	if p.mcp == nil {
		p.log.Warn("async poller disabled — MCP client not available")
		return
	}

	p.log.Info("async operation poller started", "interval", p.interval)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.log.Info("async operation poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

// poll checks all pending operations for status changes.
func (p *Poller) poll(ctx context.Context) {
	ops, err := p.db.ListAllPendingOps()
	if err != nil {
		p.log.Error("poller: failed to list pending ops", "error", err)
		return
	}
	if len(ops) == 0 {
		return
	}

	for _, op := range ops {
		if ctx.Err() != nil {
			return
		}
		p.checkOp(ctx, op)
	}
}

// statusResponse is the minimal structure parsed from download/backup status results.
type statusResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// checkOp polls the status of a single async operation.
func (p *Poller) checkOp(ctx context.Context, op database.AsyncOp) {
	toolName, args := p.statusToolFor(op)
	if toolName == "" {
		return
	}

	result, err := p.mcp.CallTool(ctx, toolName, args)
	if err != nil {
		p.log.Debug("poller: status check failed", "op_id", op.OperationID, "tool", toolName, "error", err)
		return
	}

	var status statusResponse
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		p.log.Debug("poller: failed to parse status", "op_id", op.OperationID, "result", result)
		return
	}

	switch status.Status {
	case "done", "failed":
		p.handleTerminal(op, status)
	default:
		p.handleProgress(op)
	}
}

// statusToolFor returns the MCP tool name and args to check this op's status.
func (p *Poller) statusToolFor(op database.AsyncOp) (string, map[string]interface{}) {
	switch op.ToolName {
	case "download_to_server":
		return "get_download_status", map[string]interface{}{
			"server_name": op.ServerName,
			"download_id": op.OperationID,
		}
	case "create_backup":
		return "get_backup_status", map[string]interface{}{
			"server_name": op.ServerName,
			"backup_id":   op.OperationID,
		}
	default:
		p.log.Warn("poller: unknown async tool", "tool", op.ToolName)
		return "", nil
	}
}

// handleTerminal processes a completed or failed operation.
func (p *Poller) handleTerminal(op database.AsyncOp, status statusResponse) {
	if err := p.db.UpdateAsyncOpStatus(op.OperationID, status.Status); err != nil {
		p.log.Error("poller: failed to update op status", "op_id", op.OperationID, "error", err)
		return
	}

	eventType := "async_complete"
	msg := op.Description + " completed successfully"
	if status.Status == "failed" {
		eventType = "async_failed"
		msg = op.Description + " failed"
		if status.Error != "" {
			msg += ": " + status.Error
		}
	}

	p.log.Info("async operation finished",
		"op_id", op.OperationID,
		"server", op.ServerName,
		"status", status.Status,
	)

	p.hub.Send(op.UserID, Event{
		Type:        eventType,
		OpID:        op.OperationID,
		OpType:      opTypeFromTool(op.ToolName),
		ServerName:  op.ServerName,
		Description: op.Description,
		Status:      status.Status,
		Message:     msg,
		ConvID:      op.ConversationID,
	})

	if p.OnComplete != nil {
		p.OnComplete(op, status.Status)
	}
}

// handleProgress sends an elapsed-time update for a still-running operation.
func (p *Poller) handleProgress(op database.AsyncOp) {
	elapsed := int(math.Round(time.Since(op.CreatedAt).Seconds()))

	p.hub.Send(op.UserID, Event{
		Type:        "async_progress",
		OpID:        op.OperationID,
		OpType:      opTypeFromTool(op.ToolName),
		ServerName:  op.ServerName,
		Description: op.Description,
		Status:      "pending",
		ElapsedSec:  elapsed,
		ConvID:      op.ConversationID,
	})
}

func opTypeFromTool(toolName string) string {
	switch toolName {
	case "download_to_server":
		return "download"
	case "create_backup":
		return "backup"
	default:
		return toolName
	}
}
