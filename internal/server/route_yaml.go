package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

// RouteManagerYAML looks up routes from an in-memory map loaded from YAML.
type RouteManagerYAML struct {
	routes map[string]*Route
}

func NewRouteManagerYAML(routesPath string) (*RouteManagerYAML, error) {

	routes, err := loadRoutesFromYAML(routesPath)
	if err != nil {
		return nil, err
	}

	slog.Info("using yaml route manager", "routes", len(routes))

	m := make(map[string]*Route, len(routes))
	for i := range routes {
		m[routes[i].Domain] = &routes[i]
	}

	return &RouteManagerYAML{routes: m}, nil
}

func (r *RouteManagerYAML) LookupRoute(_ context.Context, domain string) (*Route, error) {
	entry, ok := r.routes[domain]
	if !ok {
		return nil, fmt.Errorf("no route for domain %q", domain)
	}
	return entry, nil
}

func (r *RouteManagerYAML) ListenRoutes(_ context.Context) ([]Route, error) {
	var routes []Route
	for _, route := range r.routes {
		if route.Listen != "" {
			routes = append(routes, *route)
		}
	}
	return routes, nil
}

func (r *RouteManagerYAML) Close(_ context.Context) error {
	return nil
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
