// Package config loads and validates environment-based configuration.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config holds all configuration values for the chatbot.
type Config struct {
	AnthropicAPIKey string
	ClaudeModel     string
	MCPServerCmd    string
	AdminPassword   string
	SessionSecret   string
	DataDir         string
	Port            string
	LogLevel        string
}

// Load reads configuration from environment variables and validates required fields.
func Load() (*Config, error) {
	cfg := &Config{
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		ClaudeModel:     envOr("CLAUDE_MODEL", "claude-sonnet-4-6"),
		MCPServerCmd:    os.Getenv("MCP_SERVER_CMD"),
		AdminPassword:   os.Getenv("ADMIN_PASSWORD"),
		SessionSecret:   os.Getenv("SESSION_SECRET"),
		DataDir:         envOr("DATA_DIR", "/data"),
		Port:            envOr("PORT", "8080"),
		LogLevel:        envOr("LOG_LEVEL", "info"),
	}

	var missing []string
	if cfg.AnthropicAPIKey == "" {
		missing = append(missing, "ANTHROPIC_API_KEY")
	}
	if cfg.MCPServerCmd == "" {
		missing = append(missing, "MCP_SERVER_CMD")
	}
	if cfg.AdminPassword == "" {
		missing = append(missing, "ADMIN_PASSWORD")
	}
	if cfg.SessionSecret == "" {
		missing = append(missing, "SESSION_SECRET")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// SlogLevel returns the slog.Level corresponding to the configured log level.
func (c *Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
