package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/sethvargo/go-envconfig"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen     string `env:"TUNN_LISTEN, default=:6060"`
	WSListen   string `env:"TUNN_WS_LISTEN, default=:6061"`
	AgentToken string `env:"TUNN_AGENT_TOKEN, required"`
	LogLevel   string `env:"TUNN_LOG_LEVEL, default=info"`
	RoutesPath string `env:"TUNN_ROUTES_PATH, default=routes.yaml"`

	Routes []Route `env:"-"`
}

type Route struct {
	Domain  string   `yaml:"domain"`
	Service string   `yaml:"service"`
	ALPN    []string `yaml:"alpn,omitempty"`
}

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

func loadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	routes, err := loadRoutes(cfg.RoutesPath)
	if err != nil {
		return nil, err
	}
	cfg.Routes = routes

	return &cfg, nil
}

type routesFile struct {
	Routes []Route `yaml:"routes"`
}

func loadRoutes(path string) ([]Route, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read routes file: %w", err)
	}

	var f routesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse routes file: %w", err)
	}

	return f.Routes, nil
}
