package server

import (
	"fmt"
	"sync"
)

type RouteTable struct {
	routes map[string]*Route
	mu     sync.RWMutex
}

func NewRouteTable(routes []Route) *RouteTable {
	rt := &RouteTable{
		routes: make(map[string]*Route, len(routes)),
	}
	for i := range routes {
		rt.routes[routes[i].Domain] = &routes[i]
	}
	return rt
}

func (rt *RouteTable) Lookup(domain string) (*Route, error) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	entry, ok := rt.routes[domain]
	if !ok {
		return nil, fmt.Errorf("no route for domain %q", domain)
	}
	return entry, nil
}

func (rt *RouteTable) ValidateALPN(entry *Route, clientALPN []string) error {
	if len(entry.ALPN) == 0 {
		return nil // no ALPN restriction
	}

	for _, allowed := range entry.ALPN {
		for _, offered := range clientALPN {
			if allowed == offered {
				return nil
			}
		}
	}

	return fmt.Errorf("ALPN mismatch for %q: route allows %v, client offered %v", entry.Domain, entry.ALPN, clientALPN)
}
