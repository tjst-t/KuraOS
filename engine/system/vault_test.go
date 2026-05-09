package system

import (
	"bytes"
	"context"
	"testing"
)

// [AC-Ssys001-2-1] NT-hash mirrors the well-known SMB protocol vector.
// "password" -> "8846F7EAEE8FB117AD06BDD830B7586C" (NTLM RFC test vector).
func TestNTHash_KnownVector(t *testing.T) {
	cases := []struct {
		plaintext string
		want      string
	}{
		{"password", "8846F7EAEE8FB117AD06BDD830B7586C"},
		{"hello", "066DDFD4EF0E9CD7C256FE77191EF43C"},
	}
	for _, c := range cases {
		t.Run(c.plaintext, func(t *testing.T) {
			got := NTHash(c.plaintext)
			if got != c.want {
				t.Fatalf("NTHash(%q) = %s, want %s", c.plaintext, got, c.want)
			}
		})
	}
}

// [AC-Ssys001-5-2] age round-trip: encrypt then decrypt with the same
// passphrase recovers the plaintext exactly.
func TestAgeEncryptDecrypt(t *testing.T) {
	plain := []byte("kuraos vault payload — credentials secrets uid alloc")
	const passphrase = "my-very-strong-passphrase"

	var encrypted bytes.Buffer
	if err := EncryptVault(&encrypted, plain, passphrase); err != nil {
		t.Fatalf("EncryptVault: %v", err)
	}
	if encrypted.Len() == 0 {
		t.Fatalf("encrypted output is empty")
	}
	if bytes.Contains(encrypted.Bytes(), plain) {
		t.Fatalf("plaintext appears in age-encrypted output")
	}

	got, err := DecryptVault(bytes.NewReader(encrypted.Bytes()), passphrase)
	if err != nil {
		t.Fatalf("DecryptVault: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch:\nwant %q\n got %q", plain, got)
	}
}

// [AC-Ssys001-5-2] Wrong passphrase MUST fail decryption.
func TestAgeDecrypt_WrongPassphrase(t *testing.T) {
	var enc bytes.Buffer
	if err := EncryptVault(&enc, []byte("payload"), "real-passphrase"); err != nil {
		t.Fatalf("EncryptVault: %v", err)
	}
	if _, err := DecryptVault(bytes.NewReader(enc.Bytes()), "wrong-passphrase"); err == nil {
		t.Fatalf("decryption with wrong passphrase succeeded — should fail")
	}
}

// [AC-Ssys001-2-1] / [AC-Ssys001-2-2] Dual-hash write: SetUserPassword
// stores both argon2id and NT-hash in the same transaction.
func TestVaultRoundTrip_DualHash(t *testing.T) {
	ctx := context.Background()
	eng, _ := newTestEngine(t)
	const userID = "u-alice-0001"

	pt := NewPlaintextPassword("hunter2-strong-pw")
	if err := eng.SetUserPassword(ctx, userID, pt); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}

	argon, err := eng.LookupCredential(ctx, CredentialArgon2id, OwnerUser, userID)
	if err != nil {
		t.Fatalf("lookup argon2id: %v", err)
	}
	if argon.Value != "fake$hunter2-strong-pw" {
		t.Fatalf("argon2id value mismatch: %q", argon.Value)
	}
	nt, err := eng.LookupCredential(ctx, CredentialNTHash, OwnerUser, userID)
	if err != nil {
		t.Fatalf("lookup nt-hash: %v", err)
	}
	wantNT := NTHash("hunter2-strong-pw")
	if nt.Value != wantNT {
		t.Fatalf("nt-hash value mismatch: got %s want %s", nt.Value, wantNT)
	}
}

// SmbPasswdLine produces the exact stdin format pdbedit expects.
func TestSmbPasswdLine_Format(t *testing.T) {
	prevNow := smbNow
	smbNow = func() int64 { return 0x6543210F }
	defer func() { smbNow = prevNow }()

	line := SmbPasswdLine("alice", 30001, "8846F7EAEE8FB117AD06BDD830B7586C")
	wantPrefix := "alice:30001:"
	if line[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("smbpasswd line prefix: %q", line[:len(wantPrefix)])
	}
	if !contains(line, "[U          ]") {
		t.Fatalf("smbpasswd line missing user flag: %q", line)
	}
	if !contains(line, "8846F7EAEE8FB117AD06BDD830B7586C") {
		t.Fatalf("smbpasswd line missing NT hash: %q", line)
	}
	if !contains(line, "LCT-6543210F") {
		t.Fatalf("smbpasswd line missing deterministic LCT: %q", line)
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
