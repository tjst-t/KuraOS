// Package acme provides a DNS-01 challenge provider registry for ACME (lego).
//
// Design:
//   - DNSProvider is the abstraction over lego's challenge.Provider.
//   - Each provider registers itself via registry.Register("name", NewFunc)
//     using an init() in its own file under acme/providers/.
//   - v1 ships only "cloudflare". Other providers (Route53, Sakura, お名前)
//     are explicitly on the backlog (DESIGN_PRINCIPLES priority #7: staged).
//   - The UI reads the list of registered providers from this registry so it
//     automatically reflects any new provider added at compile time — no UI
//     code changes required (AC-Sf92666-1-3).
//
// DESIGN_PRINCIPLES priority #9: DNSProvider interface allows test mocks.
package acme

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// DNSProvider is the interface all DNS-01 providers must satisfy.
// It is a subset of lego's challenge.Provider to avoid making lego a hard
// transitive dependency in packages that only need to list providers.
type DNSProvider interface {
	// Present creates the DNS-01 TXT record for the given token.
	Present(ctx context.Context, domain, token, keyAuth string) error
	// CleanUp removes the DNS-01 TXT record.
	CleanUp(ctx context.Context, domain, token, keyAuth string) error
}

// NewFunc is the constructor signature for a DNS provider. It receives
// the env-sourced configuration map (e.g. {"api_token": "..."}) and
// returns a ready-to-use DNSProvider.
type NewFunc func(cfg map[string]string) (DNSProvider, error)

var (
	mu        sync.RWMutex
	providers = make(map[string]NewFunc)
)

// Register registers a DNS provider factory under name. Intended to be called
// from init() in each provider's file. Panics on duplicate registration so
// misconfigurations are caught at startup, not silently ignored.
// [AC-Sf92666-1-3]
func Register(name string, fn NewFunc) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := providers[name]; dup {
		panic(fmt.Sprintf("acme: duplicate provider registration: %q", name))
	}
	providers[name] = fn
}

// List returns the sorted names of all registered providers.
// [AC-Sf92666-1-3]
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// New constructs the named provider with the supplied config map.
// Returns ErrProviderNotFound if the name has not been registered.
func New(name string, cfg map[string]string) (DNSProvider, error) {
	mu.RLock()
	fn, ok := providers[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("acme: provider %q not registered: %w", name, ErrProviderNotFound)
	}
	return fn(cfg)
}

// ErrProviderNotFound is returned when the requested provider name has not been
// registered via Register.
var ErrProviderNotFound = fmt.Errorf("provider not found")
