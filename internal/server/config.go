package server

import (
	"context"
	"fmt"

	"github.com/lib/pq"
	"github.com/sethvargo/go-envconfig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	Listen      string `env:"TUNN_LISTEN, default=:6060"`
	WSListen    string `env:"TUNN_WS_LISTEN, default=:6061"`
	AgentToken  string `env:"TUNN_AGENT_TOKEN, required"`
	LogLevel    string `env:"TUNN_LOG_LEVEL, default=info"`
	DatabaseURL string `env:"TUNN_DATABASE_URL, required"`
}

type Route struct {
	Domain  string         `gorm:"column:domain;primaryKey"`
	Service string         `gorm:"column:service"`
	Cluster string         `gorm:"column:cluster"`
	ALPN    pq.StringArray `gorm:"column:alpn;type:text[]"`
}

func (Route) TableName() string {
	return "routes"
}

func loadConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &cfg, nil
}

func connectDB(dbURL string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	return db, nil
}
