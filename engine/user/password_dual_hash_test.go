package user_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/kuraos-org/kura/internal/store"
)

// fastHasher matches the testing convention used elsewhere — argon2id is
// too slow for unit-test budgets.
type fastHasher struct{}

func (fastHasher) Hash(pw string) (string, error)          { return "fast$" + pw, nil }
func (fastHasher) Verify(pw, encoded string) (bool, error) { return encoded == "fast$"+pw, nil }

// silentExec swallows pdbedit calls so engine/system.SetUserPassword can
// project the NT-hash without a real Samba install.
type silentExec struct{}

func (silentExec) Run(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	return nil, nil, nil
}

// [AC-Ssys001-2-1] SetPassword via engine/user + engine/system.SetUserPassword
// writes argon2id (auth_methods) and NT-hash (vault) in the same flow.
// The plaintext is delivered to engine/system once and zeroed afterwards.
func TestUserDualHashWrite_PlaintextUsedOnce(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	hasher := fastHasher{}
	users := user.NewStore(st.DB(), hasher)
	created, err := users.CreateLocalUser(ctx, "alice", "Alice", "initialPW", user.RoleUser)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	sysEng, err := system.New(system.Options{
		DB:               st.DB(),
		FS:               system.NewRealFS(t.TempDir()),
		Exec:             silentExec{},
		Hasher:           system.NewHasherAdapter(hasher),
		Users:            system.NewUserStoreAdapter(users),
		OnPasswordChange: system.LegacyAuthMethodMirror(st.DB()),
	})
	if err != nil {
		t.Fatalf("system.New: %v", err)
	}

	cases := []struct {
		name string
		pw   string
	}{
		{"ascii", "rotated-pw-1"},
		{"unicode", "とりあえずパスワード"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pt := system.NewPlaintextPassword(c.pw)
			if err := sysEng.SetUserPassword(ctx, created.ID, pt); err != nil {
				t.Fatalf("SetUserPassword: %v", err)
			}
			// Plaintext must be zeroed by the time SetUserPassword
			// returns.
			for _, b := range pt.Bytes() {
				if b != 0 {
					t.Fatalf("plaintext not zeroed: %v", pt.Bytes())
				}
			}
			// Vault contains both kinds.
			argon, err := sysEng.LookupCredential(ctx, system.CredentialArgon2id, system.OwnerUser, created.ID)
			if err != nil {
				t.Fatalf("lookup argon2id: %v", err)
			}
			if argon.Value != "fast$"+c.pw {
				t.Fatalf("argon2id mirror mismatch: %q", argon.Value)
			}
			nt, err := sysEng.LookupCredential(ctx, system.CredentialNTHash, system.OwnerUser, created.ID)
			if err != nil {
				t.Fatalf("lookup nt_hash: %v", err)
			}
			want := system.NTHash(c.pw)
			if nt.Value != want {
				t.Fatalf("nt_hash mismatch: got %s want %s", nt.Value, want)
			}
			// auth_methods has been mirrored too — VerifyPassword still works.
			_, ok, err := users.VerifyPassword(ctx, "alice", c.pw)
			if err != nil {
				t.Fatalf("VerifyPassword: %v", err)
			}
			if !ok {
				t.Fatalf("VerifyPassword false after dual-hash write")
			}
		})
	}
	_ = cmdexec.NewFake // keep import alive even if test refactor reduces usage.
}
