// Package mcp manages the MCP server subprocess lifecycle.
package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Process manages an MCP server subprocess communicating over stdio.
type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
	log    *slog.Logger
}

// Start launches the MCP server subprocess. The command string is split on spaces
// to support arguments (e.g., "/app/mcp --flag"). All current environment variables
// are passed to the subprocess.
func Start(ctx context.Context, command string, log *slog.Logger) (*Process, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty MCP_SERVER_CMD")
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr // MCP server logs to stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server: %w", err)
	}

	log.Info("MCP server subprocess started", "pid", cmd.Process.Pid, "cmd", command)

	return &Process{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		log:    log,
	}, nil
}

// Stdin returns the writer to the subprocess stdin.
func (p *Process) Stdin() io.WriteCloser {
	return p.stdin
}

// Stdout returns the reader from the subprocess stdout.
func (p *Process) Stdout() io.ReadCloser {
	return p.stdout
}

// Stop gracefully shuts down the MCP server subprocess.
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stdin != nil {
		p.stdin.Close()
	}

	if p.cmd.Process != nil {
		p.log.Info("stopping MCP server subprocess", "pid", p.cmd.Process.Pid)
		return p.cmd.Wait()
	}
	return nil
}

// Running returns true if the subprocess is still alive.
func (p *Process) Running() bool {
	return p.cmd.ProcessState == nil
}
