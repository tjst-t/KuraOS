package app

import (
	"context"
	"errors"
	"testing"

	"github.com/kuraos-org/kura/engine/system"
)

// TestPortAllocatorDeterministic covers AC-S1bccf5-3-2: ports are
// allocated deterministically and survive reopening the store.
func TestPortAllocatorDeterministic(t *testing.T) {
	// [AC-S1bccf5-3-2]
	st := mustOpenStore(t)
	alloc := NewPortAllocator(st.DB())
	alloc.Range = PortRange{Min: 50000, Max: 50010}

	res := PortReservation{AppID: "immich.0001", Container: "server", ManifestPort: 3001}

	first, err := alloc.Reserve(context.Background(), res)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if first != 50000 {
		t.Errorf("first port = %d, want 50000 (range start)", first)
	}

	// Same tuple → same port.
	second, err := alloc.Reserve(context.Background(), res)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if second != first {
		t.Errorf("second reserve gave %d, want stable %d", second, first)
	}

	// Different container in same app → next port.
	resB := PortReservation{AppID: "immich.0001", Container: "db", ManifestPort: 5432}
	bPort, err := alloc.Reserve(context.Background(), resB)
	if err != nil {
		t.Fatalf("reserve b: %v", err)
	}
	if bPort != 50001 {
		t.Errorf("second container port = %d, want 50001", bPort)
	}
}

// TestPortAllocatorSurvivesReopen confirms the SQLite-backed reservation
// survives closing + reopening the store (proxy for restart). Without the
// table the allocator would hand out 50000 again, breaking the
// "再起動を跨いで保持される" guarantee.
func TestPortAllocatorSurvivesReopen(t *testing.T) {
	// [AC-S1bccf5-3-2]
	dbPath := t.TempDir() + "/state.db"

	{
		st, err := openStoreAt(dbPath)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		alloc := NewPortAllocator(st.DB())
		alloc.Range = PortRange{Min: 50000, Max: 50010}
		got, err := alloc.Reserve(context.Background(), PortReservation{
			AppID: "immich.0001", Container: "server", ManifestPort: 3001,
		})
		if err != nil {
			t.Fatalf("first reserve: %v", err)
		}
		if got != 50000 {
			t.Fatalf("first port = %d, want 50000", got)
		}
		_ = st.Close()
	}
	{
		st, err := openStoreAt(dbPath)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer st.Close()
		alloc := NewPortAllocator(st.DB())
		alloc.Range = PortRange{Min: 50000, Max: 50010}
		got, err := alloc.Reserve(context.Background(), PortReservation{
			AppID: "immich.0001", Container: "server", ManifestPort: 3001,
		})
		if err != nil {
			t.Fatalf("second reserve: %v", err)
		}
		if got != 50000 {
			t.Errorf("after reopen port = %d, want 50000 (must survive restart)", got)
		}
	}
}

// TestPortAllocatorRangeExhausted checks the operator-visible error when
// the configured range fills.
func TestPortAllocatorRangeExhausted(t *testing.T) {
	st := mustOpenStore(t)
	alloc := NewPortAllocator(st.DB())
	alloc.Range = PortRange{Min: 60000, Max: 60001}
	for i, p := range []int{1, 2} {
		_, err := alloc.Reserve(context.Background(), PortReservation{
			AppID: "x", Container: "c", ManifestPort: p,
		})
		if err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	_, err := alloc.Reserve(context.Background(), PortReservation{
		AppID: "x", Container: "c", ManifestPort: 3,
	})
	if err == nil {
		t.Fatalf("expected exhaustion error")
	}
}

// fakeSystem is the in-memory SystemEngine used by SecretStore tests.
type fakeSystem struct {
	creds map[string]system.Credential
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{creds: map[string]system.Credential{}}
}

func (f *fakeSystem) key(kind system.CredentialKind, owner system.CredentialOwnerKind, ownerID string) string {
	return string(kind) + "|" + string(owner) + "|" + ownerID
}

func (f *fakeSystem) SetCredential(_ context.Context, c system.Credential) error {
	f.creds[f.key(c.Kind, c.OwnerKind, c.OwnerID)] = c
	return nil
}

func (f *fakeSystem) LookupCredential(_ context.Context, kind system.CredentialKind, owner system.CredentialOwnerKind, ownerID string) (system.Credential, error) {
	c, ok := f.creds[f.key(kind, owner, ownerID)]
	if !ok {
		return system.Credential{}, system.ErrUserNotFound
	}
	return c, nil
}

// TestSecretStoreEnsuresVaultBackedSecret covers AC-S1bccf5-3-2's
// implication that the internal secret is tracked deterministically and
// stored in the unified vault (priority #1).
func TestSecretStoreEnsuresVaultBackedSecret(t *testing.T) {
	// [AC-S1bccf5-3-2]
	sys := newFakeSystem()
	store := NewSecretStore(sys)

	first, err := store.EnsureSecret(context.Background(), "immich.0001", "db_password")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if len(first) < 16 {
		t.Errorf("secret too short: %d", len(first))
	}

	second, err := store.EnsureSecret(context.Background(), "immich.0001", "db_password")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if first != second {
		t.Errorf("EnsureSecret returned different secrets on repeat call: %q vs %q", first, second)
	}

	// Confirm the secret actually landed in the vault — the canonical
	// SSOT (priority #1). config.json must NOT be needed to recover this.
	cred, err := sys.LookupCredential(context.Background(), system.CredentialAppDBPassword, system.OwnerApp, "immich.0001:db_password")
	if err != nil {
		t.Fatalf("vault lookup: %v", err)
	}
	if cred.Value != first {
		t.Errorf("vault value %q != generated secret %q", cred.Value, first)
	}
}

// TestSecretStoreMissingSystemErrors guards against accidentally wiring
// a SecretStore without a SystemEngine (which would silently regenerate
// secrets every call — a security smell).
func TestSecretStoreMissingSystemErrors(t *testing.T) {
	store := &SecretStore{}
	_, err := store.EnsureSecret(context.Background(), "x", "db_password")
	if err == nil {
		t.Fatalf("expected error on missing System engine")
	}
}

// TestSecretStorePropagatesLookupErrors makes sure non-NotFound errors
// from the vault surface up rather than triggering an accidental
// regenerate.
func TestSecretStorePropagatesLookupErrors(t *testing.T) {
	failing := &errorSystem{}
	store := NewSecretStore(failing)
	_, err := store.EnsureSecret(context.Background(), "x", "db_password")
	if err == nil {
		t.Fatalf("expected propagated error")
	}
	if !errors.Is(err, errSystemBoom) {
		t.Errorf("err = %v, want wraps errSystemBoom", err)
	}
}

var errSystemBoom = errors.New("vault: boom")

type errorSystem struct{}

func (e *errorSystem) SetCredential(_ context.Context, _ system.Credential) error {
	return errSystemBoom
}

func (e *errorSystem) LookupCredential(_ context.Context, _ system.CredentialKind, _ system.CredentialOwnerKind, _ string) (system.Credential, error) {
	return system.Credential{}, errSystemBoom
}
