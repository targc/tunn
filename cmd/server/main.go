package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/targc/tunn/internal/config"
	"github.com/targc/tunn/internal/server"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	configPath := os.Getenv("TUNN_CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.LoadServerConfig(ctx, configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	slog.Info("starting tunnel server",
		"tcp", cfg.Server.Listen,
		"ws", cfg.Server.WSListen,
		"routes", len(cfg.Routes),
	)

	srv := server.New(cfg)
	if err := srv.Start(ctx); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
