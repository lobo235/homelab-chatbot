package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/lobo235/homelab-chatbot/internal/api"
	"github.com/lobo235/homelab-chatbot/internal/auth"
	"github.com/lobo235/homelab-chatbot/internal/chat"
	"github.com/lobo235/homelab-chatbot/internal/config"
	"github.com/lobo235/homelab-chatbot/internal/database"
	"github.com/lobo235/homelab-chatbot/internal/mcp"
)

// version is set at build time via -ldflags "-X main.version=<value>".
var version = "dev"

func main() {
	// Load .env if present — ignore error if file doesn't exist.
	_ = godotenv.Load()

	// Bootstrap logger at INFO until config is loaded.
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("starting homelab-chatbot", "version", version)

	cfg, err := config.Load()
	if err != nil {
		log.Error("config error", "error", err)
		os.Exit(1)
	}

	// Reconfigure logger with the level from config.
	log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))

	// Open database.
	db, err := database.Open(cfg.DataDir, log)
	if err != nil {
		log.Error("database error", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Bootstrap admin user.
	authSvc := auth.NewService(db, log)
	if err := authSvc.BootstrapAdmin(cfg.AdminPassword); err != nil {
		log.Error("admin bootstrap error", "error", err)
		os.Exit(1)
	}

	// Start MCP server subprocess.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var mcpClient *chat.MCPClient
	mcpProc, err := mcp.Start(ctx, cfg.MCPServerCmd, log)
	if err != nil {
		log.Warn("MCP server failed to start — chat will work without tools", "error", err)
	} else {
		defer func() { _ = mcpProc.Stop() }()

		mcpClient = chat.NewMCPClient(mcpProc, log)
		if err := mcpClient.Initialize(ctx); err != nil {
			log.Warn("MCP initialization failed — tools unavailable", "error", err)
			mcpClient = nil
		} else {
			tools, err := mcpClient.ListTools(ctx)
			if err != nil {
				log.Warn("MCP tool listing failed", "error", err)
			} else {
				log.Info("MCP tools available", "count", len(tools))
			}
		}
	}

	// Create chat service.
	chatSvc := chat.NewService(cfg.AnthropicAPIKey, cfg.ClaudeModel, mcpProc, log)

	// Create and start HTTP server.
	srv := api.NewServer(db, authSvc, chatSvc, mcpClient, cfg.Gateways, version, log)

	addr := ":" + cfg.Port
	if err := srv.Run(ctx, addr); err != nil {
		log.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}
