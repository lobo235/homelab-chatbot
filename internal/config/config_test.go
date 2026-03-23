package config

import (
	"log/slog"
	"os"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("MCP_SERVER_CMD", "/app/mcp")
	t.Setenv("ADMIN_PASSWORD", "admin123")
	t.Setenv("SESSION_SECRET", "secret123")
}

func TestLoad_AllRequired(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AnthropicAPIKey != "test-key" {
		t.Errorf("got AnthropicAPIKey=%q, want %q", cfg.AnthropicAPIKey, "test-key")
	}
	if cfg.MCPServerCmd != "/app/mcp" {
		t.Errorf("got MCPServerCmd=%q, want %q", cfg.MCPServerCmd, "/app/mcp")
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	// Clear all required vars.
	for _, key := range []string{"ANTHROPIC_API_KEY", "MCP_SERVER_CMD", "ADMIN_PASSWORD", "SESSION_SECRET"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required vars")
	}
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ClaudeModel != "claude-sonnet-4-6" {
		t.Errorf("got ClaudeModel=%q, want %q", cfg.ClaudeModel, "claude-sonnet-4-6")
	}
	if cfg.DataDir != "/data" {
		t.Errorf("got DataDir=%q, want %q", cfg.DataDir, "/data")
	}
	if cfg.Port != "8080" {
		t.Errorf("got Port=%q, want %q", cfg.Port, "8080")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("got LogLevel=%q, want %q", cfg.LogLevel, "info")
	}
}

func TestSlogLevel(t *testing.T) {
	tests := []struct {
		level string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
	}
	for _, tt := range tests {
		cfg := &Config{LogLevel: tt.level}
		if got := cfg.SlogLevel(); got != tt.want {
			t.Errorf("SlogLevel(%q) = %v, want %v", tt.level, got, tt.want)
		}
	}
}
