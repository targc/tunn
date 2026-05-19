package config

import (
	"context"
	"fmt"
	"os"

	"github.com/sethvargo/go-envconfig"
	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Server ServerSettings `yaml:"server" env:", prefix=TUNN_"`
	Routes []Route        `yaml:"routes"`
}

type ServerSettings struct {
	Listen     string `yaml:"listen"      env:"LISTEN, default=:6060"`
	WSListen   string `yaml:"ws_listen"   env:"WS_LISTEN, default=:6061"`
	AgentToken string `yaml:"agent_token" env:"AGENT_TOKEN"`
	LogLevel   string `yaml:"log_level"   env:"LOG_LEVEL, default=info"`
}

type Route struct {
	Domain  string   `yaml:"domain"`
	Service string   `yaml:"service"`
	ALPN    []string `yaml:"alpn,omitempty"`
}

type AgentConfig struct {
	ServerURL    string `env:"TUNN_SERVER_URL, required"`
	AgentToken   string `env:"TUNN_AGENT_TOKEN, required"`
	LogLevel     string `env:"TUNN_LOG_LEVEL, default=info"`
	ReconnectMax string `env:"TUNN_RECONNECT_MAX, default=30s"`
}

func LoadServerConfig(ctx context.Context, path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg ServerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:           &cfg.Server,
		DefaultOverwrite: true,
	}); err != nil {
		return nil, fmt.Errorf("failed to process env overrides: %w", err)
	}

	return &cfg, nil
}

func LoadAgentConfig(ctx context.Context) (*AgentConfig, error) {
	var cfg AgentConfig
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load agent config: %w", err)
	}
	return &cfg, nil
}
