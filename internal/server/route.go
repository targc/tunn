package server

import (
	"context"
	"fmt"

	"github.com/lib/pq"
)

type Route struct {
	Domain  string         `yaml:"domain"  gorm:"column:domain;primaryKey"`
	Service string         `yaml:"service" gorm:"column:service"`
	Cluster string         `yaml:"cluster" gorm:"column:cluster"`
	ALPN    pq.StringArray `yaml:"alpn,omitempty" gorm:"column:alpn;type:text[]"`
}

func (Route) TableName() string {
	return "routes"
}

type IRouteManager interface {
	LookupRoute(ctx context.Context, domain string) (*Route, error)
	Close(ctx context.Context) error
}

func buildRouteManager(cfg *Config) (IRouteManager, error) {
	if cfg.DatabaseURL != "" {
		return NewRouteManagerPostgres(cfg.DatabaseURL)
	}

	if cfg.RoutesPath != "" {
		return NewRouteManagerYAML(cfg.RoutesPath)
	}

	return nil, fmt.Errorf("either TUNN_ROUTES_PATH or TUNN_DATABASE_URL must be set")
}
