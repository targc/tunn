package server

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func connectDB(dbURL string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	return db, nil
}

// RouteManagerPostgres queries the database on each lookup.
type RouteManagerPostgres struct {
	db *gorm.DB
}

func NewRouteManagerPostgres(databaseURL string) (*RouteManagerPostgres, error) {

	db, err := connectDB(databaseURL)
	if err != nil {
		return nil, err
	}

	slog.Info("using postgres route manager")

	return &RouteManagerPostgres{db: db}, nil
}

func (r *RouteManagerPostgres) LookupRoute(ctx context.Context, domain string) (*Route, error) {
	var route Route
	err := r.db.
		WithContext(ctx).
		Where("domain = ?", domain).
		First(&route).
		Error

	if err != nil {
		return nil, fmt.Errorf("no route for domain %q: %w", domain, err)
	}

	return &route, nil
}

func (r *RouteManagerPostgres) Close(ctx context.Context) error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}
