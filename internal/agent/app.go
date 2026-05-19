package agent

import (
	"context"
	"log/slog"
)

type App struct {
	Config *Config
	Agent  *TunnelAgent
}

func NewApp(ctx context.Context) (*App, error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	slog.Info("tunnel agent configured", "server", cfg.ServerURL, "cluster", cfg.ClusterID)

	return &App{
		Config: cfg,
		Agent:  New(cfg),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.Agent.Run(ctx)
}
