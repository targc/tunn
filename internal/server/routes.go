package server

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

func lookupRoute(ctx context.Context, db *gorm.DB, domain string) (*Route, error) {
	var route Route
	err := db.
		WithContext(ctx).
		Where("domain = ?", domain).
		First(&route).
		Error

	if err != nil {
		return nil, fmt.Errorf("no route for domain %q: %w", domain, err)
	}
	return &route, nil
}

func validateALPN(route *Route, clientALPN []string) error {
	if len(route.ALPN) == 0 {
		return nil
	}

	for _, allowed := range route.ALPN {
		for _, offered := range clientALPN {
			if allowed == offered {
				return nil
			}
		}
	}

	return fmt.Errorf("ALPN mismatch for %q: route allows %v, client offered %v", route.Domain, route.ALPN, clientALPN)
}
