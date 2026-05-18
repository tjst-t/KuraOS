package main

// files_wiring.go — wires the File API and built-in filebrowser into kura (S0eedaa).
//
// Wiring strategy:
//   1. Create a production osBackend (real filesystem).
//   2. Build a shareResolver that wraps the share.Engine (share name → path).
//   3. Build a userSharesFn that resolves the session user → ACL-permitted shares.
//   4. Wire the Files page handler (/ui/files) into the UI renderer.
//   5. Wire the File API handler (/api/files/*) into the gateway.
//
// The File API uses X-Kura-Token HMAC authentication; for the built-in
// filebrowser the gateway's session auth middleware protects /ui/files and
// the userSharesFn does per-request ACL filtering.

import (
	"context"
	"net/http"
	"os"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/fileapi"
	"github.com/kuraos-org/kura/internal/ui"
)

// shareEngineResolver adapts share.Manager to fileapi.ShareResolver.
// It resolves a share name to its filesystem path by listing all shares.
type shareEngineResolver struct {
	engine *share.Manager
}

func (r *shareEngineResolver) ResolveShareRoot(ctx context.Context, shareName string) (string, error) {
	shares, err := r.engine.List(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range shares {
		if s.Name == shareName {
			return s.Path, nil
		}
	}
	return "", fileapi.ErrNoScope // re-use as "not found" sentinel
}

// buildUserSharesFn returns a function that, given an HTTP request, returns
// the share names the session user has read access to (ACL evaluation).
//
// [AC-S0eedaa-2-2]: only shows shares the current user's ACL permits.
func buildUserSharesFn(
	sessions *session.Store,
	users *user.Store,
	shareEngine *share.Manager,
) func(r *http.Request) []string {
	return func(r *http.Request) []string {
		// Resolve session cookie → user.
		cookie, err := r.Cookie(session.CookieName)
		if err != nil {
			return nil
		}
		sess, err := sessions.Lookup(r.Context(), cookie.Value)
		if err != nil {
			return nil
		}
		u, err := users.GetByID(r.Context(), sess.UserID)
		if err != nil {
			return nil
		}

		allShares, err := shareEngine.List(r.Context())
		if err != nil {
			return nil
		}

		// Admin sees all shares.
		if u.Role == user.RoleAdmin {
			names := make([]string, 0, len(allShares))
			for _, s := range allShares {
				if !s.Disabled {
					names = append(names, s.Name)
				}
			}
			return names
		}

		// Regular user: include shares where their username appears in the ACL
		// with mode != "none". Group membership is v1.x (requires system engine
		// group-membership query).
		var accessible []string
		for _, s := range allShares {
			if s.Disabled {
				continue
			}
			// No ACL → default access mode applies.
			if len(s.ACL) == 0 {
				if s.AccessMode == share.AccessReadOnly || s.AccessMode == share.AccessReadWrite {
					accessible = append(accessible, s.Name)
				}
				continue
			}
			for _, entry := range s.ACL {
				if entry.Kind == share.PrincipalUser && entry.Name == u.Username &&
					entry.Mode != share.ACLModeNone {
					accessible = append(accessible, s.Name)
					break
				}
			}
		}
		return accessible
	}
}

// buildFilesWiring constructs the FilesDeps for the UI renderer and the
// File API handler for the gateway.
func buildFilesWiring(
	shareEngine *share.Manager,
	sessions *session.Store,
	users *user.Store,
) (ui.FilesDeps, http.Handler) {
	backend := fileapi.NewOSBackend()
	resolver := &shareEngineResolver{engine: shareEngine}
	userSharesFn := buildUserSharesFn(sessions, users, shareEngine)

	filesDeps := ui.FilesDeps{
		Backend:      backend,
		Shares:       resolver,
		UserShares:   userSharesFn,
		UseRealPaths: true,
	}

	// File API handler for native apps using X-Kura-Token.
	// [AC-S0eedaa-1-2]: HMAC key management.
	sigKey := fileAPISigningKey()
	tokenValidator := &fileapi.HMACValidator{Key: sigKey}

	// Scope store: permissive for v1. The SQLite-backed scope store (per-app
	// scope declaration in manifest) is wired in v1.x when config.json gains
	// a scopes[] block. For now, the app manifest's shares[] already declares
	// intent at install time.
	scopeStore := &permissiveScopeStore{}

	apiHandler := fileapi.NewHandler(fileapi.HandlerConfig{
		Backend:      backend,
		Validator:    tokenValidator,
		Scopes:       scopeStore,
		Shares:       resolver,
		UseRealPaths: true,
	})

	return filesDeps, apiHandler.Routes()
}

// fileAPISigningKey returns the HMAC-SHA256 key for X-Kura-Token.
// In production this should come from the vault (DESIGN_PRINCIPLES priority #1:
// アプリの内部秘密を config.json export 時に平文で含めない). For v1 we use
// KURA_FILE_API_KEY env var as an escape hatch; if absent a static dev default
// is used (only safe on LAN NAS behind firewall).
func fileAPISigningKey() []byte {
	if v := os.Getenv("KURA_FILE_API_KEY"); v != "" {
		return []byte(v)
	}
	// Dev default — not a real secret; obviously do not use in production.
	return []byte("kuraos-dev-file-api-key-change-me")
}

// permissiveScopeStore grants access to any share for any app.
// Used in v1 until the per-app SQLite scope store lands.
// nil Shares in AppScope means "all shares permitted" (see EvalScope).
type permissiveScopeStore struct{}

func (permissiveScopeStore) GetScope(_ context.Context, _ string) (fileapi.AppScope, error) {
	return fileapi.AppScope{Shares: nil}, nil
}
