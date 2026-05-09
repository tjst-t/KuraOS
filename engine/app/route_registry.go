package app

import (
	"sync"
)

// MemoryRouteRegistry is the in-memory RouteRegistry used by both production
// (the gateway reads its current state on every request) and tests (assert
// on the registered set after Install / Uninstall).
//
// Safe for concurrent use.
type MemoryRouteRegistry struct {
	mu     sync.RWMutex
	routes map[string]AppRoute
}

// NewMemoryRouteRegistry returns an empty registry.
func NewMemoryRouteRegistry() *MemoryRouteRegistry {
	return &MemoryRouteRegistry{routes: map[string]AppRoute{}}
}

// RegisterAppRoute stores route keyed by AppID.
func (r *MemoryRouteRegistry) RegisterAppRoute(route AppRoute) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[route.AppID] = route
	return nil
}

// UnregisterAppRoute removes the route for appID. Tolerates missing entries.
func (r *MemoryRouteRegistry) UnregisterAppRoute(appID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, appID)
	return nil
}

// ListAppRoutes returns all registered routes.
func (r *MemoryRouteRegistry) ListAppRoutes() []AppRoute {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AppRoute, 0, len(r.routes))
	for _, rt := range r.routes {
		out = append(out, rt)
	}
	return out
}

// LookupRoute returns the route matching appID. ok=false when missing.
func (r *MemoryRouteRegistry) LookupRoute(appID string) (AppRoute, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.routes[appID]
	return rt, ok
}

// LookupByName returns the first route matching appName. Used by the gateway
// path-mode handler to resolve /apps/<name>/.
func (r *MemoryRouteRegistry) LookupByName(name string) (AppRoute, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, rt := range r.routes {
		if rt.AppName == name {
			return rt, true
		}
	}
	return AppRoute{}, false
}

var _ RouteRegistry = (*MemoryRouteRegistry)(nil)
