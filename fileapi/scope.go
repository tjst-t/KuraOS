package fileapi

import (
	"context"
	"fmt"
	"strings"
)

// AppScope declares what a native app is allowed to access through the File API.
// Loaded from the app_scopes table (SSOT via config.json / SQLite).
type AppScope struct {
	// Shares is the allowlist of share names the app may access.
	// An empty list means no access.
	Shares []string `json:"shares"`
	// PathDeny is a list of path prefixes denied within the allowed shares.
	// Evaluated after the share allowlist passes.
	PathDeny []string `json:"path_deny"`
}

// ScopeStore retrieves the scope for a named app.
// Production uses SQLiteShareScopeStore; tests use FixedScopeStore.
type ScopeStore interface {
	// GetScope returns the AppScope for appName.
	// Returns ErrNoScope when the app has no declared scope (access denied).
	GetScope(ctx context.Context, appName string) (AppScope, error)
}

// ErrNoScope is returned when an app has no registered scope.
var ErrNoScope = fmt.Errorf("fileapi: no scope registered for app")

// EvalScope evaluates whether the app may access (shareName, path).
// Returns nil on grant, non-nil on deny.
//
// The two-step evaluation matches design.md §6.4:
//  1. Share allowlist: appScope.Shares must contain shareName.
//     A nil Shares slice means "allow all shares" (used by the permissive store).
//  2. PathDeny prefixes: none of the prefixes must match path
func EvalScope(scope AppScope, shareName, path string) error {
	// Step 1: share allowlist. nil means "all shares permitted".
	if scope.Shares != nil {
		allowed := false
		for _, s := range scope.Shares {
			if s == shareName {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("fileapi: app not permitted to access share %q", shareName)
		}
	}
	// Step 2: path_deny.
	clean := strings.TrimPrefix(path, "/")
	for _, deny := range scope.PathDeny {
		prefix := strings.TrimPrefix(deny, "/")
		if prefix == "" {
			continue
		}
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return fmt.Errorf("fileapi: path %q denied by app scope rule %q", path, deny)
		}
	}
	return nil
}

// FixedScopeStore returns the same scope for every app. For tests.
type FixedScopeStore struct {
	Scope AppScope
}

func (f *FixedScopeStore) GetScope(_ context.Context, _ string) (AppScope, error) {
	return f.Scope, nil
}

// DenyScopeStore always returns ErrNoScope. For negative tests.
type DenyScopeStore struct{}

func (DenyScopeStore) GetScope(_ context.Context, _ string) (AppScope, error) {
	return AppScope{}, ErrNoScope
}
