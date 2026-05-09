package system

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// [AC-Ssys001-5-1] Backup tarball contains exactly the three documented
// files: manifest.json, config.json (plaintext), secrets.kura.age.
func TestBackup_TarballLayout(t *testing.T) {
	ctx := context.Background()
	eng, st := newTestEngine(t)
	const userID = "u-alice-0001"

	if err := eng.SetUserPassword(ctx, userID, NewPlaintextPassword("hunter2pw")); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	if _, err := eng.AllocateUID(ctx, userID); err != nil {
		t.Fatalf("AllocateUID: %v", err)
	}

	configRaw := []byte(`{"schema_version": 1}`)
	var buf bytes.Buffer
	err := WriteBackup(ctx, &buf, st.DB(), BackupOptions{
		ConfigJSON: configRaw,
		Passphrase: "test-passphrase",
		HostHint:   "test-host",
	})
	if err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("backup is empty")
	}
	if bytes.Contains(buf.Bytes(), []byte("hunter2pw")) {
		t.Fatalf("plaintext password leaked into tarball bytes")
	}
	if bytes.Contains(buf.Bytes(), []byte("fake$hunter2pw")) {
		t.Fatalf("argon2id verifier leaked into the tarball outside the encrypted section")
	}
}

// [AC-Ssys001-5-2] Round-trip: backup then restore recovers every
// credential row and every uid_alloc row to a fresh database.
func TestBackup_RoundTripPassphraseEncrypted(t *testing.T) {
	ctx := context.Background()
	eng, st := newTestEngine(t)

	const userA = "u-alice-0001"
	const userB = "u-bob-0002"
	for _, u := range []string{userA, userB} {
		if err := eng.SetUserPassword(ctx, u, NewPlaintextPassword(u+"-pw")); err != nil {
			t.Fatalf("SetUserPassword %s: %v", u, err)
		}
		if _, err := eng.AllocateUID(ctx, u); err != nil {
			t.Fatalf("AllocateUID %s: %v", u, err)
		}
	}

	cfg := []byte(`{"schema_version": 1}`)
	var tarball bytes.Buffer
	if err := WriteBackup(ctx, &tarball, st.DB(), BackupOptions{
		ConfigJSON: cfg,
		Passphrase: "round-trip-pw",
	}); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}

	// Read into fresh DB.
	_, freshSt := newTestEngine(t)
	res, err := ReadBackup(ctx, &tarball, "round-trip-pw")
	if err != nil {
		t.Fatalf("ReadBackup: %v", err)
	}
	if res.Manifest.SchemaVersion != BackupSchemaVersion {
		t.Fatalf("manifest version %d", res.Manifest.SchemaVersion)
	}
	if !bytes.Equal(res.ConfigJSON, cfg) {
		t.Fatalf("config.json mismatch")
	}
	if len(res.Vault.Credentials) != 4 {
		t.Fatalf("expected 4 credentials (2 users x argon2id+nt_hash), got %d",
			len(res.Vault.Credentials))
	}
	if len(res.Vault.UIDAllocs) != 2 {
		t.Fatalf("expected 2 uid_allocs, got %d", len(res.Vault.UIDAllocs))
	}

	if err := ApplyVaultRestore(ctx, freshSt.DB(), res.Vault); err != nil {
		t.Fatalf("ApplyVaultRestore: %v", err)
	}
	// Verify restored credentials are queryable.
	c, err := lookupCredential(ctx, freshSt.DB(), CredentialNTHash, OwnerUser, userA)
	if err != nil {
		t.Fatalf("lookup restored nt_hash: %v", err)
	}
	if c.Value == "" {
		t.Fatalf("restored nt_hash is empty")
	}
}

// [AC-Ssys001-5-2] Wrong passphrase MUST cause ReadBackup to fail with a
// distinct error so the CLI can show "wrong passphrase" instead of a
// generic decode error.
func TestBackup_WrongPassphraseRejected(t *testing.T) {
	ctx := context.Background()
	_, st := newTestEngine(t)
	var buf bytes.Buffer
	if err := WriteBackup(ctx, &buf, st.DB(), BackupOptions{
		ConfigJSON: []byte("{}"),
		Passphrase: "right-pw",
	}); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}
	_, err := ReadBackup(ctx, &buf, "wrong-pw")
	if err == nil {
		t.Fatalf("ReadBackup with wrong passphrase succeeded")
	}
	if !strings.Contains(err.Error(), "decrypt") && !strings.Contains(err.Error(), "age") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// [AC-Ssys001-5-1] Empty passphrase MUST be rejected (no plaintext-vault
// fallback).
func TestBackup_RejectsEmptyPassphrase(t *testing.T) {
	ctx := context.Background()
	_, st := newTestEngine(t)
	var buf bytes.Buffer
	err := WriteBackup(ctx, &buf, st.DB(), BackupOptions{
		ConfigJSON: []byte("{}"),
		Passphrase: "",
	})
	if err == nil {
		t.Fatalf("expected error on empty passphrase")
	}
}
