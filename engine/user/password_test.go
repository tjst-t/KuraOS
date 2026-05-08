package user

import (
	"strings"
	"testing"
)

// [AC-S1e7eeb-1-2] パスワードは argon2id でハッシュ化され平文で保存されない.
func TestHasher_RoundTrip(t *testing.T) {
	h := NewHasherWith(Argon2idParams{Time: 1, Memory: 8 * 1024, Threads: 1, SaltLen: 16, KeyLen: 32})
	cases := []struct {
		name string
		pw   string
	}{
		{"ascii", "correct horse battery staple"},
		{"japanese", "パスワード123"},
		{"long", strings.Repeat("a", 256)},
		{"emoji", "p4ss\xf0\x9f\x94\x91"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded, err := h.Hash(c.pw)
			if err != nil {
				t.Fatalf("Hash: %v", err)
			}
			if strings.Contains(encoded, c.pw) {
				t.Fatalf("encoded leaks plaintext password")
			}
			if !strings.HasPrefix(encoded, "$argon2id$") {
				t.Fatalf("encoded missing argon2id prefix: %q", encoded)
			}
			ok, err := h.Verify(c.pw, encoded)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !ok {
				t.Fatalf("Verify returned false for matching password")
			}
			ok, err = h.Verify(c.pw+"x", encoded)
			if err != nil {
				t.Fatalf("Verify wrong: %v", err)
			}
			if ok {
				t.Fatalf("Verify returned true for wrong password")
			}
		})
	}
}

func TestHasher_DifferentSaltsProduceDifferentHashes(t *testing.T) {
	h := NewHasherWith(Argon2idParams{Time: 1, Memory: 8 * 1024, Threads: 1, SaltLen: 16, KeyLen: 32})
	a, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash a: %v", err)
	}
	b, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash b: %v", err)
	}
	if a == b {
		t.Fatalf("two hashes of the same password must differ (random salt)")
	}
}

func TestHasher_RejectsMalformed(t *testing.T) {
	h := NewHasher()
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$",
		"$argon2id$v=19$m=19456,t=2,p=1$bad-base64!!$bad",
	}
	for _, encoded := range cases {
		t.Run(encoded, func(t *testing.T) {
			_, err := h.Verify("anything", encoded)
			if err == nil {
				t.Fatalf("expected error on malformed verifier")
			}
		})
	}
}

func TestHasher_EmptyPasswordRejected(t *testing.T) {
	h := NewHasher()
	if _, err := h.Hash(""); err == nil {
		t.Fatalf("expected error hashing empty password")
	}
}
