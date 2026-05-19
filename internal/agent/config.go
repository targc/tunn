package agent

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	ServerURL    string `env:"TUNN_SERVER_URL, required"`
	AgentToken   string `env:"TUNN_AGENT_TOKEN, required"`
	ClusterID    string `env:"TUNN_CLUSTER_ID, required"`
	LogLevel     string `env:"TUNN_LOG_LEVEL, default=info"`
	ReconnectMax string `env:"TUNN_RECONNECT_MAX, default=30s"`
}

func loadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &cfg, nil
}
