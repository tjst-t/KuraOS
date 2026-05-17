// federation_wiring.go: bootstraps the external IdP RP layer (Google in
// v1, others in v1.x). Lives here for the same dependency-direction
// reason as oidc_wiring.go.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"os"

	"github.com/kuraos-org/kura/engine/auth/federation"
	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"
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
func buildFederationHandler(ctx context.Context, db *sql.DB, sysEng system.Engine, users *user.Store, sessions *session.Store, errRenderer federation.ErrorRenderer) http.Handler {
	storage := oidc.NewStorage(db)
	mgr := federation.New(storage, sessions, &credentialAdapter{eng: sysEng})
	mgr.SetProvisioner(&fedProvisioner{db: db, sys: sysEng, users: users})
	mgr.SetRoleLookup(&fedRoleLookup{users: users})
	if errRenderer != nil {
		mgr.SetErrorRenderer(errRenderer)
	}

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

// fedProvisioner implements federation.UserProvisioner by routing the
// new-user creation through engine/system.Engine.CreateUser so the full
// projection chain runs (uid_alloc + argon2id + NT-hash + /etc/passwd
// reconcile). priority #10: no engine may call useradd / chown / smbpasswd
// directly; everything goes through engine/system.
//
// Federated users have no operator-known password, so we mint a strong
// random one and stash it in the vault. The user can sign in via Google
// without ever knowing it; if they want to enable SMB later, the admin
// (or the user themselves, when a self-service screen lands in v1.x)
// resets it from the UI.
type fedProvisioner struct {
	db    *sql.DB
	sys   system.Engine
	users *user.Store
}

func (p *fedProvisioner) CreateFromFederation(ctx context.Context, username, displayName, email string) (string, error) {
	// De-dup: if a user with this username already exists (admin
	// pre-created them and chose the same name), return that ID and let
	// federation_links bind to it instead of creating a duplicate. This
	// matches the previous handler's ON CONFLICT semantics.
	if existing, err := p.users.GetByUsername(ctx, username); err == nil {
		return existing.ID, nil
	}
	pw, err := randomPassword(24)
	if err != nil {
		return "", err
	}
	id, err := p.sys.CreateUser(ctx, system.CreateUserInput{
		Username:    username,
		DisplayName: displayName,
		Password:    pw,
		Role:        string(user.RolePending),
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// randomPassword returns a base64url-encoded, n-byte random string.
// Used as the placeholder credential for auto-provisioned federated
// users (Sfix002-3). The bytes never leave engine/system after Hash;
// only the argon2id verifier + NT-hash are persisted.
func randomPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := randomRead(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// fedRoleLookup satisfies federation.RoleLookup so the callback can pick
// /ui/admin/dashboard vs /ui after issuing a session for the federated
// user (Sfix002-1).
type fedRoleLookup struct{ users *user.Store }

func (r *fedRoleLookup) LookupRole(ctx context.Context, userID string) (string, error) {
	if r.users == nil {
		return "", nil
	}
	u, err := r.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return string(u.Role), nil
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
