package server

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	Listen      string `env:"TUNN_LISTEN, default=:6060"`
	WSListen    string `env:"TUNN_WS_LISTEN, default=:6061"`
	AgentToken  string `env:"TUNN_AGENT_TOKEN, required"`
	LogLevel    string `env:"TUNN_LOG_LEVEL, default=info"`
	RoutesPath  string `env:"TUNN_ROUTES_PATH"`
	DatabaseURL string `env:"TUNN_DATABASE_URL"`
	TLSCert     string `env:"TUNN_TLS_CERT"`
	TLSKey      string `env:"TUNN_TLS_KEY"`
}

func loadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if cfg.RoutesPath == "" && cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("either TUNN_ROUTES_PATH or TUNN_DATABASE_URL must be set")
	}
	return &cfg, nil
}
