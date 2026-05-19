package server

import "fmt"

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
