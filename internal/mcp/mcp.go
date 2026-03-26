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

// allowedEnvKeys lists environment variables that may be passed to the MCP
// server subprocess. Everything else (SESSION_SECRET, ADMIN_PASSWORD, etc.)
// is deliberately excluded.
var allowedEnvKeys = map[string]bool{
	"ANTHROPIC_API_KEY":          true,
	"DISCOVERY_TEMP_DIR":         true,
	"NOMAD_GATEWAY_URL":          true,
	"NOMAD_GATEWAY_KEY":          true,
	"ADGUARD_GATEWAY_URL":        true,
	"ADGUARD_GATEWAY_KEY":        true,
	"CF_GATEWAY_URL":             true,
	"CF_GATEWAY_KEY":             true,
	"MINECRAFT_GATEWAY_URL":      true,
	"MINECRAFT_GATEWAY_KEY":      true,
	"CURSEFORGE_GATEWAY_URL":     true,
	"CURSEFORGE_GATEWAY_KEY":     true,
	"VAULT_GATEWAY_URL":          true,
	"VAULT_GATEWAY_KEY":          true,
	"DATA_DIR":                   true,
	"LOG_LEVEL":                  true,
	"NOMAD_DEFAULT_DATACENTER":   true,
	"NOMAD_DEFAULT_NODE_POOL":    true,
	"NFS_BASE_PATH":              true,
	"MC_PUBLIC_DOMAIN":           true,
	"MC_PUBLIC_IP":               true,
	"CF_ZONE_NAME":               true,
	"ARTIFACT_ALLOWLIST":         true,
	"ITZG_DOCS_REFRESH_INTERVAL": true,
	"PATH":                       true,
	"HOME":                       true,
}

// filteredEnv returns only the allowed environment variables for the MCP subprocess.
func filteredEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if allowedEnvKeys[key] {
			env = append(env, kv)
		}
	}
	return env
}

// Start launches the MCP server subprocess. The command string is split on spaces
// to support arguments (e.g., "/app/mcp --flag"). Only MCP-relevant environment
// variables are passed to the subprocess — secrets like ANTHROPIC_API_KEY,
// SESSION_SECRET, and ADMIN_PASSWORD are excluded.
func Start(ctx context.Context, command string, log *slog.Logger) (*Process, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty MCP_SERVER_CMD")
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Env = filteredEnv()
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
