package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/targc/tunn/internal/server"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app, err := server.NewApp(ctx)
	if err != nil {
		slog.Error("failed to initialize", "err", err)
		os.Exit(1)
	}

	if err := app.Run(ctx); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
