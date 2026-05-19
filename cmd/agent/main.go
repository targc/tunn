package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/targc/tunn/internal/agent"
	"github.com/targc/tunn/internal/config"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.LoadAgentConfig(ctx)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	slog.Info("starting tunnel agent", "server", cfg.ServerURL)

	a := agent.New(cfg)
	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("agent error", "err", err)
		os.Exit(1)
	}
}
