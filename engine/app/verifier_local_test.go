package app

import (
	"context"
	"errors"
	"testing"
)

// TestLocalVerifierHappyPath signs a payload and verifies it back through
// the LocalVerifier — covers the operator-managed signing key flow that is
// the v1 production verifier (sigstore-go full integration deferred).
func TestLocalVerifierHappyPath(t *testing.T) {
	pub, priv, keyID, err := GenerateLocalKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	v := NewLocalVerifier().WithTrustedKey(keyID, pub)

	payload := []byte("manifest payload")
	bundle, err := SignBlobLocal(payload, priv, keyID, "kuraos-local:operator", "kuraos-local")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := v.VerifyBlob(context.Background(), payload, bundle, "kuraos-local:.*", "kuraos-local"); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestLocalVerifierFailClosed enumerates the negative paths.
func TestLocalVerifierFailClosed(t *testing.T) {
	pub, priv, keyID, _ := GenerateLocalKeypair()
	v := NewLocalVerifier().WithTrustedKey(keyID, pub)
	payload := []byte("p")
	bundle, _ := SignBlobLocal(payload, priv, keyID, "kuraos-local:foo", "kuraos-local")

	cases := []struct {
		name           string
		payload        []byte
		bundle         []byte
		identityRegex  string
		issuer         string
		wantErrIs      error
		wantErrSubstr  string
	}{
		{"empty payload", nil, bundle, "kuraos-local:.*", "", ErrSignatureVerification, ""},
		{"empty bundle", payload, nil, "kuraos-local:.*", "", ErrSignatureVerification, ""},
		{"empty regex", payload, bundle, "", "", ErrSignatureVerification, ""},
		{"identity mismatch", payload, bundle, "evil:.*", "", ErrIdentityMismatch, ""},
		{"issuer mismatch", payload, bundle, "kuraos-local:.*", "other", ErrSignatureVerification, "issuer"},
		{"tampered payload", []byte("tamper"), bundle, "kuraos-local:.*", "", ErrSignatureVerification, "ed25519 verify"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := v.VerifyBlob(context.Background(), c.payload, c.bundle, c.identityRegex, c.issuer)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if c.wantErrIs != nil && !errors.Is(err, c.wantErrIs) {
				t.Fatalf("err = %v; want is %v", err, c.wantErrIs)
			}
		})
	}
}
