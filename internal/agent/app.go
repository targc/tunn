package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	ServerURL    string `env:"TUNN_SERVER_URL, required"`
	AgentToken   string `env:"TUNN_AGENT_TOKEN, required"`
	ClusterID    string `env:"TUNN_CLUSTER_ID, required"`
	LogLevel     string `env:"TUNN_LOG_LEVEL, default=info"`
	ReconnectMax string `env:"TUNN_RECONNECT_MAX, default=30s"`
}

type App struct {
	Config *Config
	Agent  *TunnelAgent
}

func NewApp(ctx context.Context) (*App, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	slog.Info("tunnel agent configured", "server", cfg.ServerURL, "cluster", cfg.ClusterID)

	return &App{
		Config: &cfg,
		Agent:  New(&cfg),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.Agent.Run(ctx)
}
