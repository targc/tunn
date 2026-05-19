package server

import (
	"context"
	"log/slog"
)

type App struct {
	Config *Config
	Server *TunnelServer
}

func NewApp(ctx context.Context) (*App, error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	slog.Info("tunnel server configured",
		"tcp", cfg.Listen,
		"ws", cfg.WSListen,
		"routes", len(cfg.Routes),
	)

	return &App{
		Config: cfg,
		Server: New(cfg),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.Server.Start(ctx)
}
