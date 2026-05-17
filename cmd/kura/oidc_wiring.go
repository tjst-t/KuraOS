// oidc_wiring.go: bootstraps the in-house OIDC OP and the engine/app
// OIDCRegistrar adapter. Lives here (not in engine/auth/oidc) so the
// engine package stays unaware of engine/system and engine/user — those
// imports would create dependency cycles for tests that mock individual
// pieces.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/kuraos-org/kura/engine/app"
	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"
)

// buildOIDCProvider wires the OIDC OP from its dependencies. Returns nil
// (and logs) if anything required is missing — startup tolerates the
// absence so dev builds without OIDC env vars still come up.
func buildOIDCProvider(ctx context.Context, db *sql.DB, sysEng system.Engine, sessions *session.Store, users *user.Store) *oidc.Provider {
	issuer := os.Getenv("KURA_OIDC_ISSUER")
	if issuer == "" {
		// Default to the gateway's HTTP origin. /oidc/* paths are
		// appended at request time. Operators override via env when
		// they have a real public URL.
		issuer = "http://localhost:" + os.Getenv("KURA_PORT")
	}
	storage := oidc.NewStorage(db)
	key, err := oidc.EnsureSigningKey(ctx, &credentialAdapter{eng: sysEng})
	if err != nil {
		fmt.Fprintf(os.Stderr, "oidc: ensure signing key: %v (oidc disabled)\n", err)
		return nil
	}

	p := oidc.New(issuer, storage, key,
		&sessionResolverAdapter{sessions: sessions, users: users},
		&userInfoAdapter{users: users},
	)
	p.RoleLookup = &oidcRoleLookupAdapter{users: users}
	p.SecretLookup = func(ctx context.Context, clientID string) (string, error) {
		cred, err := sysEng.LookupCredential(ctx,
			system.CredentialOIDCClientSecret, system.OwnerApp, clientID)
		if err != nil {
			return "", err
		}
		return cred.Value, nil
	}
	return p
}

// credentialAdapter bridges engine/system.Engine into oidc.CredentialStore.
// Wraps SetCredential / LookupCredential so engine/auth/oidc never imports
// engine/system directly.
type credentialAdapter struct{ eng system.Engine }

func (a *credentialAdapter) LookupCredential(ctx context.Context, kind, ownerKind, ownerID string) (string, error) {
	cred, err := a.eng.LookupCredential(ctx,
		system.CredentialKind(kind),
		system.CredentialOwnerKind(ownerKind),
		ownerID)
	if err != nil {
		if errors.Is(err, system.ErrUserNotFound) {
			return "", err
		}
		return "", err
	}
	return cred.Value, nil
}

func (a *credentialAdapter) SetCredential(ctx context.Context, kind, ownerKind, ownerID, value string) error {
	return a.eng.SetCredential(ctx, system.Credential{
		Kind:      system.CredentialKind(kind),
		OwnerKind: system.CredentialOwnerKind(ownerKind),
		OwnerID:   ownerID,
		Value:     value,
	})
}

// sessionResolverAdapter implements oidc.SessionResolver by deferring to
// the session store + user lookup. Returns ok=false silently for any
// missing/expired session so the OP redirects to /login.
type sessionResolverAdapter struct {
	sessions *session.Store
	users    *user.Store
}

func (a *sessionResolverAdapter) ResolveSession(ctx context.Context, cookie string) (string, bool, error) {
	if a.sessions == nil {
		return "", false, nil
	}
	sess, err := a.sessions.Lookup(ctx, cookie)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if a.users != nil {
		u, err := a.users.GetByID(ctx, sess.UserID)
		if err == nil && u.Disabled {
			return "", false, nil
		}
	}
	return sess.UserID, true, nil
}

// userInfoAdapter pulls profile/email claims from engine/user for the
// id_token + /userinfo response.
type userInfoAdapter struct{ users *user.Store }

func (a *userInfoAdapter) GetClaims(ctx context.Context, userID string) (map[string]any, error) {
	if a.users == nil {
		return map[string]any{}, nil
	}
	u, err := a.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	claims := map[string]any{
		"preferred_username": u.Username,
		"name":               u.DisplayName,
	}
	if u.DisplayName == "" {
		claims["name"] = u.Username
	}
	return claims, nil
}

// oidcRoleLookupAdapter implements oidc.UserRoleLookup so the OIDC OP can
// reject pending users before issuing an auth-code.
type oidcRoleLookupAdapter struct{ users *user.Store }

func (a *oidcRoleLookupAdapter) LookupRole(ctx context.Context, userID string) (string, error) {
	if a.users == nil {
		return "", nil
	}
	u, err := a.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return string(u.Role), nil
}

// appOIDCRegistrar implements app.OIDCRegistrar by delegating to the
// OIDC storage + the credential vault. Constructed once at startup and
// passed into AppLifecycle.OIDC.
type appOIDCRegistrar struct {
	op      *oidc.Provider
	sysEng  system.Engine
	gateway string // public origin used to build absolute redirect URIs
}

func newAppOIDCRegistrar(op *oidc.Provider, sysEng system.Engine, gateway string) *appOIDCRegistrar {
	return &appOIDCRegistrar{op: op, sysEng: sysEng, gateway: gateway}
}

func (r *appOIDCRegistrar) RegisterAppClient(ctx context.Context, req app.OIDCAppClient) (app.OIDCAppClientCreds, error) {
	if r.op == nil {
		return app.OIDCAppClientCreds{}, errors.New("oidc: provider not wired")
	}
	clientID := "app-" + req.AppID
	secret := oidc.NewClientSecret()
	// Translate relative redirect URIs (/apps/foo/callback) into absolute
	// URLs. The OIDC spec requires absolute URIs in the registration but
	// the manifest only knows the relative path.
	abs := make([]string, 0, len(req.RedirectURIs))
	for _, ru := range req.RedirectURIs {
		if len(ru) > 0 && (ru[0] == 'h' || ru[0] == 'H') {
			abs = append(abs, ru)
		} else {
			abs = append(abs, r.gateway+ru)
		}
	}
	if _, err := r.op.Storage.RegisterClient(ctx, oidc.Client{
		ClientID:     clientID,
		Name:         req.AppName,
		RedirectURIs: abs,
		AppID:        req.AppID,
	}); err != nil {
		return app.OIDCAppClientCreds{}, fmt.Errorf("register oidc client: %w", err)
	}
	if err := r.sysEng.SetCredential(ctx, system.Credential{
		Kind:      system.CredentialOIDCClientSecret,
		OwnerKind: system.OwnerApp,
		OwnerID:   clientID,
		Value:     secret,
	}); err != nil {
		return app.OIDCAppClientCreds{}, fmt.Errorf("vault store secret: %w", err)
	}
	return app.OIDCAppClientCreds{
		ClientID:     clientID,
		ClientSecret: secret,
		Issuer:       r.op.Issuer,
		RedirectURI:  abs[0],
	}, nil
}

func (r *appOIDCRegistrar) UnregisterAppClient(ctx context.Context, appID string) error {
	if r.op == nil {
		return nil
	}
	clientID := "app-" + appID
	_ = r.op.Storage.DeleteClient(ctx, clientID)
	// Vault entry: ignore "not found", surface only real errors. Vault
	// has no Delete API today (entries are upserted), so we leave the row
	// in place — it's harmless after the client is gone.
	_ = clientID
	return nil
}

// gatewayPublicOrigin computes the absolute origin the gateway is reachable
// at. Used to materialise absolute redirect_uri values for OIDC clients.
// Falls back to http://localhost:<port> when not explicitly set.
func gatewayPublicOrigin() string {
	if v := os.Getenv("KURA_PUBLIC_ORIGIN"); v != "" {
		return v
	}
	return "http://localhost:" + os.Getenv("KURA_PORT")
}

// withCredentialAdapter is a convenience for the federation wiring.
func wrapCredentials(eng system.Engine) *credentialAdapter {
	return &credentialAdapter{eng: eng}
}

// silence unused imports during incremental build
var _ http.Handler
