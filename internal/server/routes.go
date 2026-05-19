package server

import (
	"fmt"
	"sync"

	"github.com/targc/tunn/internal/config"
)

type RouteEntry struct {
	Domain  string
	Service string
	ALPN    []string
}

type RouteTable struct {
	routes map[string]*RouteEntry
	mu     sync.RWMutex
}

func NewRouteTable(routes []config.Route) *RouteTable {
	rt := &RouteTable{
		routes: make(map[string]*RouteEntry, len(routes)),
	}
	for _, r := range routes {
		rt.routes[r.Domain] = &RouteEntry{
			Domain:  r.Domain,
			Service: r.Service,
			ALPN:    r.ALPN,
		}
	}
	return rt
}

func (rt *RouteTable) Lookup(domain string) (*RouteEntry, error) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	entry, ok := rt.routes[domain]
	if !ok {
		return nil, fmt.Errorf("no route for domain %q", domain)
	}
	return entry, nil
}

func (rt *RouteTable) ValidateALPN(entry *RouteEntry, clientALPN []string) error {
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
