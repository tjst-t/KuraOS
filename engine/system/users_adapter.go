package system

import (
	"context"
	"errors"
	"fmt"

	"github.com/kuraos-org/kura/engine/user"
)

// UserStoreAdapter wraps engine/user.Store as a UserSource so engine/system
// can enumerate users without a hard import on the user package's full
// surface. Defined here (consumer side) per the project convention of
// "interface where used".
type UserStoreAdapter struct {
	store *user.Store
}

// NewUserStoreAdapter returns a UserSource backed by the given user.Store.
func NewUserStoreAdapter(store *user.Store) *UserStoreAdapter {
	return &UserStoreAdapter{store: store}
}

func (a *UserStoreAdapter) List(ctx context.Context) ([]SourceUser, error) {
	users, err := a.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("system: list users from store: %w", err)
	}
	out := make([]SourceUser, 0, len(users))
	for _, u := range users {
		out = append(out, SourceUser{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        string(u.Role),
			Disabled:    u.Disabled,
		})
	}
	return out, nil
}

// Create inserts a new user row + password verifier in engine/user. Argon2id
// hashing is delegated to the engine/user Hasher; engine/system computes
// the NT-hash mirror separately during SetUserPassword.
func (a *UserStoreAdapter) Create(ctx context.Context, in SourceCreateUser) (SourceUser, error) {
	role := user.Role(in.Role)
	if !role.Valid() {
		return SourceUser{}, fmt.Errorf("system: invalid role %q", in.Role)
	}
	u, err := a.store.CreateLocalUser(ctx, in.Username, in.DisplayName, in.Password, role)
	if err != nil {
		return SourceUser{}, fmt.Errorf("system: create user via store: %w", err)
	}
	return SourceUser{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Role:        string(u.Role),
		Disabled:    u.Disabled,
	}, nil
}

func (a *UserStoreAdapter) Update(ctx context.Context, userID, displayName, role string) error {
	r := user.Role(role)
	if !r.Valid() {
		return fmt.Errorf("system: invalid role %q", role)
	}
	if _, err := a.store.UpdateUser(ctx, userID, displayName, r); err != nil {
		return fmt.Errorf("system: update user via store: %w", err)
	}
	return nil
}

func (a *UserStoreAdapter) Delete(ctx context.Context, userID string) error {
	if err := a.store.DeleteUser(ctx, userID); err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("system: delete user via store: %w", err)
	}
	return nil
}

func (a *UserStoreAdapter) ListGroups(ctx context.Context) ([]SourceGroup, error) {
	groups, err := a.store.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("system: list groups from store: %w", err)
	}
	out := make([]SourceGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, SourceGroup{ID: g.ID, Name: g.Name, Description: g.Description})
	}
	return out, nil
}

func (a *UserStoreAdapter) CreateGroup(ctx context.Context, name, description string) (SourceGroup, error) {
	g, err := a.store.CreateGroup(ctx, name, description)
	if err != nil {
		return SourceGroup{}, fmt.Errorf("system: create group via store: %w", err)
	}
	return SourceGroup{ID: g.ID, Name: g.Name, Description: g.Description}, nil
}

func (a *UserStoreAdapter) DeleteGroup(ctx context.Context, groupID string) error {
	if err := a.store.DeleteGroup(ctx, groupID); err != nil {
		return fmt.Errorf("system: delete group via store: %w", err)
	}
	return nil
}

func (a *UserStoreAdapter) SetGroupMembers(ctx context.Context, groupID string, userIDs []string) error {
	if err := a.store.SetGroupMembers(ctx, groupID, userIDs); err != nil {
		return fmt.Errorf("system: set group members via store: %w", err)
	}
	return nil
}

func (a *UserStoreAdapter) MembersOfGroup(ctx context.Context, groupID string) ([]string, error) {
	members, err := a.store.MembersOfGroup(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("system: members of group via store: %w", err)
	}
	return members, nil
}

// HasherAdapter wraps engine/user.Hasher to satisfy system.Hasher.
// Same interface signature, but kept distinct so engine/user doesn't have
// to depend on engine/system.
type HasherAdapter struct {
	h user.Hasher
}

func NewHasherAdapter(h user.Hasher) HasherAdapter { return HasherAdapter{h: h} }

func (a HasherAdapter) Hash(password string) (string, error) { return a.h.Hash(password) }
