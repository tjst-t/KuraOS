// federation_wiring.go: bootstraps the external IdP RP layer (Google in
// v1, others in v1.x). Lives here for the same dependency-direction
// reason as oidc_wiring.go.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"

	"github.com/kuraos-org/kura/engine/auth/federation"
	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/system"
)

// randomRead is rand.Read aliased so federation_wiring tests can swap it
// for a deterministic source.
var randomRead = rand.Read

// hexEncode is hex.EncodeToString aliased for symmetry with randomRead.
var hexEncode = hex.EncodeToString

// buildFederationHandler reads the operator's federation config (env vars
// in v1; config.json migration is separate work) and returns the
// /federation/* HTTP handler. Returns nil if no providers are configured —
// the gateway then leaves the path unmounted so /federation/* 404s.
func buildFederationHandler(ctx context.Context, db *sql.DB, sysEng system.Engine, sessions *session.Store) http.Handler {
	storage := oidc.NewStorage(db)
	mgr := federation.New(storage, sessions, &credentialAdapter{eng: sysEng})
	mgr.SetProvisioner(&fedProvisioner{db: db, sys: sysEng})

	// Discover providers from the env. Each provider's config is read from
	// KURA_FED_<NAME>_CLIENT_ID, with the secret looked up from the vault
	// (kind=federation_client_secret, owner=system, owner_id=<provider>).
	// Mock provider URL (for tests) is read from KURA_FED_<NAME>_ISSUER —
	// production Google uses https://accounts.google.com.
	for _, name := range []string{"google", "github", "microsoft"} {
		clientID := os.Getenv("KURA_FED_" + envName(name) + "_CLIENT_ID")
		if clientID == "" {
			continue
		}
		issuer := os.Getenv("KURA_FED_" + envName(name) + "_ISSUER")
		if issuer == "" {
			issuer = defaultIssuer(name)
		}
		// Secret resolution order: env wins when set, otherwise fall
		// back to the vault. If env is set AND differs from vault,
		// the env value is re-persisted so a future start without env
		// uses the latest value. Older logic let vault silently
		// shadow env, so changing the env var was a no-op after the
		// first persist — caused 2026-05-12 incident where a mock
		// dev secret kept being sent to Google even after the env was
		// updated to the real Google secret.
		envSecret := os.Getenv("KURA_FED_" + envName(name) + "_CLIENT_SECRET")
		vaultCred, vaultErr := sysEng.LookupCredential(ctx,
			system.CredentialFederationClientSec, system.OwnerSystem, name)
		var secret string
		switch {
		case envSecret != "":
			secret = envSecret
			if vaultErr != nil || vaultCred.Value != envSecret {
				_ = sysEng.SetCredential(ctx, system.Credential{
					Kind:      system.CredentialFederationClientSec,
					OwnerKind: system.OwnerSystem,
					OwnerID:   name,
					Value:     envSecret,
				})
			}
		case vaultErr == nil:
			secret = vaultCred.Value
		case !errors.Is(vaultErr, system.ErrUserNotFound):
			// real lookup error — surface to logs but don't crash.
			continue
		}
		auto := os.Getenv("KURA_FED_"+envName(name)+"_AUTO_PROVISION") == "1"
		if err := mgr.Register(ctx, federation.Provider{
			Name:          name,
			Issuer:        issuer,
			ClientID:      clientID,
			ClientSecret:  secret,
			RedirectURI:   gatewayPublicOrigin() + "/federation/" + name + "/callback",
			Scopes:        []string{"openid", "email", "profile"},
			AutoProvision: auto,
		}); err != nil {
			// log and skip
			continue
		}
	}
	if mgr.Empty() {
		return nil
	}
	return mgr.Routes()
}

func envName(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		out[i] = c
	}
	return string(out)
}

// fedProvisioner implements federation.UserProvisioner by inserting a
// new user row into the users table and triggering AllocateUID via
// engine/system. Lives here so engine/auth/federation never imports
// engine/user / engine/system directly.
type fedProvisioner struct {
	db  *sql.DB
	sys system.Engine
}

func (p *fedProvisioner) CreateFromFederation(ctx context.Context, username, displayName, email string) (string, error) {
	// User row insertion: minimal columns. The existing engine/user
	// schema has more fields, but for a federated user we don't need a
	// password hash row at all. The sub becomes the unique identifier;
	// the username is just a friendly label.
	id := newUserID()
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, created_at, updated_at, disabled)
		VALUES (?, ?, ?, 'user', strftime('%Y-%m-%dT%H:%M:%fZ','now'),
		        strftime('%Y-%m-%dT%H:%M:%fZ','now'), 0)
		ON CONFLICT(username) DO UPDATE SET display_name = excluded.display_name
	`, id, username, displayName)
	if err != nil {
		return "", err
	}
	if _, err := p.sys.AllocateUID(ctx, id); err != nil {
		// non-fatal — the user can still log in even if uid allocation
		// fails (uid only matters for SMB / NFS / Docker).
		_ = err
	}
	return id, nil
}

// newUserID returns a 12-byte random hex string suitable as engine/user.User.ID.
func newUserID() string {
	var b [12]byte
	if _, err := randomRead(b[:]); err != nil {
		panic("federation: rand: " + err.Error())
	}
	return hexEncode(b[:])
}

func defaultIssuer(name string) string {
	switch name {
	case "google":
		return "https://accounts.google.com"
	case "github":
		return "https://github.com"
	case "microsoft":
		return "https://login.microsoftonline.com/common/v2.0"
	}
	return ""
}
