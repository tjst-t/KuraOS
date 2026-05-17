// users_wiring.go: adapters that translate engine/user, engine/auth/oidc,
// and the federation provider list into the consumer-side interfaces
// internal/ui.UsersDeps consumes. Living here keeps internal/ui free of
// engine imports (priority #4 — modular monolith with clean boundaries).
package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"

	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/internal/ui"
)

// currentUserFromSession returns the resolver the Users CRUD handler
// uses to enforce "admin can't delete themselves". Reads the
// kura_session cookie, looks up the session row, and returns the
// matching user id + username. ok=false on any failure.
func currentUserFromSession(sessions *session.Store, users *user.Store) func(*http.Request) (string, string, bool) {
	return func(req *http.Request) (string, string, bool) {
		if sessions == nil || users == nil {
			return "", "", false
		}
		c, err := req.Cookie(session.CookieName)
		if err != nil || c.Value == "" {
			return "", "", false
		}
		s, err := sessions.Lookup(req.Context(), c.Value)
		if err != nil {
			return "", "", false
		}
		u, err := users.GetByID(req.Context(), s.UserID)
		if err != nil {
			return "", "", false
		}
		return u.ID, u.Username, true
	}
}

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

// groupsListerAdapter wraps engine/user.Store into ui.GroupsLister so the
// Groups tab can render real (not synthetic) rows. Members are returned
// as user IDs; the UI resolves them to usernames using the user list.
type groupsListerAdapter struct{ users *user.Store }

func (a *groupsListerAdapter) ListGroups(ctx context.Context) ([]ui.GroupRow, error) {
	if a.users == nil {
		return nil, nil
	}
	groups, err := a.users.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ui.GroupRow, 0, len(groups))
	for _, g := range groups {
		members, _ := a.users.MembersOfGroup(ctx, g.ID)
		out = append(out, ui.GroupRow{
			ID:          g.ID,
			Name:        g.Name,
			Description: g.Description,
			Members:     members,
		})
	}
	return out, nil
}

// systemEngineAdapter exposes engine/system.Engine through the
// ui.SystemEngine surface. The conversion is purely a type swap on
// CreateUser to bridge ui.SystemCreateUserInput -> system.CreateUserInput.
type systemEngineAdapter struct {
	eng system.Engine
}

func (a *systemEngineAdapter) CreateUser(ctx context.Context, in ui.SystemCreateUserInput) (string, error) {
	return a.eng.CreateUser(ctx, system.CreateUserInput{
		Username: in.Username, DisplayName: in.DisplayName, Password: in.Password, Role: in.Role,
	})
}

func (a *systemEngineAdapter) UpdateUser(ctx context.Context, userID, displayName, role string) error {
	return a.eng.UpdateUser(ctx, userID, displayName, role)
}

func (a *systemEngineAdapter) DeleteUser(ctx context.Context, userID string) error {
	return a.eng.DeleteUser(ctx, userID)
}

func (a *systemEngineAdapter) CreateGroup(ctx context.Context, name, description string) (string, error) {
	return a.eng.CreateGroup(ctx, name, description)
}

func (a *systemEngineAdapter) DeleteGroup(ctx context.Context, groupID string) error {
	return a.eng.DeleteGroup(ctx, groupID)
}

func (a *systemEngineAdapter) SetGroupMembers(ctx context.Context, groupID string, userIDs []string) error {
	return a.eng.SetGroupMembers(ctx, groupID, userIDs)
}

func (a *systemEngineAdapter) PromoteFromPending(ctx context.Context, userID, newRole string) (string, error) {
	return a.eng.PromoteFromPending(ctx, userID, newRole)
}

// principalSourceAdapter exposes engine/user usernames + group names
// for the Shares ACL row picker (Sfix001-3).
type principalSourceAdapter struct {
	users *user.Store
}

func (a *principalSourceAdapter) ListUsernames(ctx context.Context) ([]string, error) {
	if a.users == nil {
		return nil, nil
	}
	all, err := a.users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for _, u := range all {
		out = append(out, u.Username)
	}
	return out, nil
}

func (a *principalSourceAdapter) ListGroupNames(ctx context.Context) ([]string, error) {
	if a.users == nil {
		return nil, nil
	}
	groups, err := a.users.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Name)
	}
	return out, nil
}

// shareACLLookupAdapter wraps engine/share.Engine into ui.ShareACLLookup
// so the Groups delete handler can refuse a delete when a Share still
// references the group.
type shareACLLookupAdapter struct {
	shares share.Engine
}

func (a *shareACLLookupAdapter) SharesUsingGroup(ctx context.Context, groupName string) ([]string, error) {
	if a.shares == nil {
		return nil, nil
	}
	all, err := a.shares.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range all {
		for _, ace := range s.ACL {
			if ace.Kind == share.PrincipalGroup && ace.Name == groupName {
				out = append(out, s.Name)
				break
			}
		}
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
