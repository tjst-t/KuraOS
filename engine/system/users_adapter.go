package system

import (
	"context"
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
			Disabled:    u.Disabled,
		})
	}
	return out, nil
}

// HasherAdapter wraps engine/user.Hasher to satisfy system.Hasher.
// Same interface signature, but kept distinct so engine/user doesn't have
// to depend on engine/system.
type HasherAdapter struct {
	h user.Hasher
}

func NewHasherAdapter(h user.Hasher) HasherAdapter { return HasherAdapter{h: h} }

func (a HasherAdapter) Hash(password string) (string, error) { return a.h.Hash(password) }
