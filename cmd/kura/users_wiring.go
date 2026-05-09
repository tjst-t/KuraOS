// users_wiring.go: adapters that translate engine/user, engine/auth/oidc,
// and the federation provider list into the consumer-side interfaces
// internal/ui.UsersDeps consumes. Living here keeps internal/ui free of
// engine imports (priority #4 — modular monolith with clean boundaries).
package main

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/internal/ui"
)

// usersListerAdapter wraps engine/user.Store into ui.UsersLister.
type usersListerAdapter struct{ users *user.Store }

func (a *usersListerAdapter) List(ctx context.Context) ([]ui.UsersUserRow, error) {
	if a.users == nil {
		return nil, nil
	}
	all, err := a.users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ui.UsersUserRow, 0, len(all))
	for _, u := range all {
		row := ui.UsersUserRow{
			UserID:      u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        string(u.Role),
			Methods:     []string{"local"},
		}
		out = append(out, row)
	}
	return out, nil
}

// federationLookupAdapter wraps oidc.Storage so the Users page can look
// up federation links per user.
type federationLookupAdapter struct{ storage *oidc.Storage }

func (a *federationLookupAdapter) ListFor(ctx context.Context, userID string) ([]ui.UsersFederationRow, error) {
	if a.storage == nil {
		return nil, nil
	}
	links, err := a.storage.ListFederationsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]ui.UsersFederationRow, 0, len(links))
	for _, l := range links {
		out = append(out, ui.UsersFederationRow{
			Provider: l.Provider,
			Subject:  l.Subject,
			Email:    l.Email,
			LinkedAt: l.LinkedAt,
		})
	}
	return out, nil
}

// oidcClientListAdapter returns a lister backed by oidc.Storage.
type oidcClientListAdapterFn func(ctx context.Context) ([]ui.UsersOIDCClient, error)

func (f oidcClientListAdapterFn) ListClients(ctx context.Context) ([]ui.UsersOIDCClient, error) {
	return f(ctx)
}

func oidcClientListAdapter(db *sql.DB) ui.OIDCClientLister {
	storage := oidc.NewStorage(db)
	return oidcClientListAdapterFn(func(ctx context.Context) ([]ui.UsersOIDCClient, error) {
		clients, err := storage.ListClients(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]ui.UsersOIDCClient, 0, len(clients))
		for _, c := range clients {
			out = append(out, ui.UsersOIDCClient{
				ClientID:     c.ClientID,
				Name:         c.Name,
				RedirectURIs: c.RedirectURIs,
				Issuer:       "auto-generated",
				CreatedAt:    c.CreatedAt,
			})
		}
		return out, nil
	})
}

// providersListAdapter returns the list of registered federation
// providers. v1 reads them from env vars (the same set
// federation_wiring.go reads), so this adapter just mirrors the env to
// avoid passing the federation manager around.
type providersListAdapter struct{ db *sql.DB }

func (a *providersListAdapter) ListProviders(_ context.Context) ([]ui.UsersProvider, error) {
	out := []ui.UsersProvider{}
	for _, name := range []string{"google", "github", "microsoft"} {
		clientID := os.Getenv("KURA_FED_" + envName(name) + "_CLIENT_ID")
		if clientID == "" {
			continue
		}
		out = append(out, ui.UsersProvider{
			Name:          name,
			Enabled:       true,
			ClientID:      clientID,
			AutoProvision: os.Getenv("KURA_FED_"+envName(name)+"_AUTO_PROVISION") == "1",
		})
	}
	return out, nil
}

// silence import linter
var _ = errors.New
