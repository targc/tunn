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

	if !db.Migrator().HasTable(&Route{}) {
		if err := db.Migrator().CreateTable(&Route{}); err != nil {
			return nil, fmt.Errorf("failed to create routes table: %w", err)
		}
		slog.Info("created routes table")
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

func (r *RouteManagerPostgres) ListenRoutes(ctx context.Context) ([]Route, error) {
	var routes []Route
	err := r.db.
		WithContext(ctx).
		Where("listen IS NOT NULL AND listen != ''").
		Find(&routes).
		Error

	if err != nil {
		return nil, fmt.Errorf("failed to query listen routes: %w", err)
	}

	return routes, nil
}

func (r *RouteManagerPostgres) Close(ctx context.Context) error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}
