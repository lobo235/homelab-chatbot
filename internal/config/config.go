// Package config loads and validates environment-based configuration.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// GatewayConfig holds URL and API key for a single gateway service.
type GatewayConfig struct {
	Name string
	URL  string
	Key  string
}

// Config holds all configuration values for the chatbot.
type Config struct {
	AnthropicAPIKey   string
	ClaudeModel       string
	MCPServerCmd      string
	AdminPassword     string
	SessionSecret     string
	DataDir           string
	Port              string
	LogLevel          string
	ContextWindowSize int
	ToolResultMaxLen  int
	Gateways          []GatewayConfig
}

// Load reads configuration from environment variables and validates required fields.
func Load() (*Config, error) {
	cfg := &Config{
		AnthropicAPIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		ClaudeModel:       envOr("CLAUDE_MODEL", "claude-sonnet-4-6"),
		MCPServerCmd:      os.Getenv("MCP_SERVER_CMD"),
		AdminPassword:     os.Getenv("ADMIN_PASSWORD"),
		SessionSecret:     os.Getenv("SESSION_SECRET"),
		DataDir:           envOr("DATA_DIR", "/data"),
		Port:              envOr("PORT", "8080"),
		LogLevel:          envOr("LOG_LEVEL", "info"),
		ContextWindowSize: envOrInt("CONTEXT_WINDOW_SIZE", 20),
		ToolResultMaxLen:  envOrInt("TOOL_RESULT_MAX_LEN", 500),
	}

	// Load gateway configs from env vars (same ones passed to MCP subprocess).
	gatewayDefs := []struct {
		name   string
		urlEnv string
		keyEnv string
	}{
		{"nomad", "NOMAD_GATEWAY_URL", "NOMAD_GATEWAY_KEY"},
		{"adguard", "ADGUARD_GATEWAY_URL", "ADGUARD_GATEWAY_KEY"},
		{"cloudflare", "CF_GATEWAY_URL", "CF_GATEWAY_KEY"},
		{"minecraft", "MINECRAFT_GATEWAY_URL", "MINECRAFT_GATEWAY_KEY"},
		{"curseforge", "CURSEFORGE_GATEWAY_URL", "CURSEFORGE_GATEWAY_KEY"},
		{"vault", "VAULT_GATEWAY_URL", "VAULT_GATEWAY_KEY"},
	}
	for _, gw := range gatewayDefs {
		if u := os.Getenv(gw.urlEnv); u != "" {
			cfg.Gateways = append(cfg.Gateways, GatewayConfig{
				Name: gw.name,
				URL:  u,
				Key:  os.Getenv(gw.keyEnv),
			})
		}
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

// Gateway returns the GatewayConfig with the given name, or nil if not configured.
func (c *Config) Gateway(name string) *GatewayConfig {
	for i := range c.Gateways {
		if c.Gateways[i].Name == name {
			return &c.Gateways[i]
		}
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
