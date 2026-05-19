package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/lib/pq"
	"github.com/sethvargo/go-envconfig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen      string `env:"TUNN_LISTEN, default=:6060"`
	WSListen    string `env:"TUNN_WS_LISTEN, default=:6061"`
	AgentToken  string `env:"TUNN_AGENT_TOKEN, required"`
	LogLevel    string `env:"TUNN_LOG_LEVEL, default=info"`
	RoutesPath  string `env:"TUNN_ROUTES_PATH"`
	DatabaseURL string `env:"TUNN_DATABASE_URL"`

	Routes []Route `env:"-"`
}

type Route struct {
	Domain  string         `yaml:"domain"  gorm:"column:domain;primaryKey"`
	Service string         `yaml:"service" gorm:"column:service"`
	Cluster string         `yaml:"cluster" gorm:"column:cluster"`
	ALPN    pq.StringArray `yaml:"alpn,omitempty" gorm:"column:alpn;type:text[]"`
}

func (Route) TableName() string {
	return "routes"
}

func loadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.RoutesPath == "" && cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("either TUNN_ROUTES_PATH or TUNN_DATABASE_URL must be set")
	}

	var routes []Route

	if cfg.RoutesPath != "" {
		yamlRoutes, err := loadRoutesFromYAML(cfg.RoutesPath)
		if err != nil {
			return nil, err
		}
		routes = append(routes, yamlRoutes...)
		slog.Info("loaded routes from yaml", "count", len(yamlRoutes))
	}

	if cfg.DatabaseURL != "" {
		dbRoutes, err := loadRoutesFromDB(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		routes = append(routes, dbRoutes...)
		slog.Info("loaded routes from database", "count", len(dbRoutes))
	}

	cfg.Routes = routes
	return &cfg, nil
}

type routesFile struct {
	Routes []Route `yaml:"routes"`
}

func loadRoutesFromYAML(path string) ([]Route, error) {
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

func loadRoutesFromDB(ctx context.Context, dbURL string) ([]Route, error) {
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}
	defer sqlDB.Close()

	var routes []Route
	err = db.
		WithContext(ctx).
		Find(&routes).
		Error

	if err != nil {
		return nil, fmt.Errorf("failed to query routes: %w", err)
	}

	return routes, nil
}
