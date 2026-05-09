package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// CosignVerifier verifies a payload against a sigstore bundle using
// cosign keyless rules: the signing certificate's SAN identity must match
// identityRegex, the OIDC issuer must match issuer, and the signature
// itself must verify against the payload bytes.
//
// The interface boundary keeps engine/app testable without a network or
// TUF root: tests inject FakeVerifier; production wires the sigstore-go
// implementation in NewSigstoreVerifier.
type CosignVerifier interface {
	VerifyBlob(ctx context.Context, payload, bundle []byte, identityRegex, issuer string) error
}

// ErrSignatureVerification is returned when signature checks fail. The
// caller distinguishes "verifier rejected the signature" from "no
// signature provided" via errors.Is on this sentinel — both map to the
// same operator-visible outcome (install/update aborted) per design.md
// §7.9 検証失敗時の挙動 and DESIGN_PRINCIPLES forbidden ("検証失敗時は
// インストール / 更新を即停止").
var ErrSignatureVerification = errors.New("app: signature verification failed")

// ErrIdentityMismatch signals the certificate identity regex did not
// match. Callers in S65b510 may want to surface this distinctly in the
// install error banner.
var ErrIdentityMismatch = errors.New("app: signing identity does not match trust policy")

// sigstoreVerifier is the production CosignVerifier seam. The actual
// sigstore-go bundle decoding + TUF root wiring lands in S65b510 once a
// real fixture bundle exists; for v1 this implementation refuses every
// call so production *must* either inject a wired verifier from S65b510
// or run with the operator-installed registry left unconfigured. This
// upholds DESIGN_PRINCIPLES forbidden ("検証失敗時はインストール /
// 更新を即停止") — never silently approve.
type sigstoreVerifier struct{}

// NewSigstoreVerifier returns the placeholder verifier described above.
// S65b510 swaps the body for the real sigstore-go pipeline once a
// reproducible bundle fixture is checked in.
func NewSigstoreVerifier() CosignVerifier {
	return &sigstoreVerifier{}
}

// VerifyBlob is the v1 placeholder. It rejects every call so production
// fails closed until S65b510 wires the real sigstore-go path.
func (v *sigstoreVerifier) VerifyBlob(_ context.Context, payload, bundleBytes []byte, identityRegex, issuer string) error {
	if identityRegex == "" {
		return fmt.Errorf("%w: identity_regex is empty", ErrSignatureVerification)
	}
	if len(bundleBytes) == 0 || len(payload) == 0 {
		return fmt.Errorf("%w: empty payload or bundle", ErrSignatureVerification)
	}
	_ = issuer
	return fmt.Errorf("%w: sigstore-go bundle verification not yet wired (TUF root + fixture pending S65b510)", ErrSignatureVerification)
}

// CompileIdentityRegex is a small helper that callers use up-front so a
// malformed regex in app_registries[].trust.identity_regex fails at
// config-load time rather than at install time.
func CompileIdentityRegex(s string) (*regexp.Regexp, error) {
	if s == "" {
		return nil, fmt.Errorf("app: identity_regex is empty")
	}
	r, err := regexp.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("app: identity_regex %q: %w", s, err)
	}
	return r, nil
}

// FakeVerifier is the in-process CosignVerifier used by tests. It accepts
// a static map of (payloadHash → identity, issuer) and approves only when
// both identity matches identityRegex and issuer equals the configured
// issuer. Any payload whose hash is not in the map is rejected.
type FakeVerifier struct {
	// Records maps "first 8 hex of sha256(payload)" → record.
	// Tests usually set Records via NewFakeVerifier or by direct
	// assignment.
	Records map[string]FakeRecord
}

// FakeRecord is what a FakeVerifier remembers about a signed payload.
type FakeRecord struct {
	Identity string
	Issuer   string
}

// VerifyBlob implements CosignVerifier. It mirrors the production rule
// set: identity must match identityRegex, issuer must match the
// requested issuer, and the payload must be one the verifier was told
// about (simulating "the signature is valid for this exact bytes").
func (f *FakeVerifier) VerifyBlob(_ context.Context, payload, _ []byte, identityRegex, issuer string) error {
	if f == nil || f.Records == nil {
		return fmt.Errorf("%w: fake verifier has no records", ErrSignatureVerification)
	}
	rec, ok := f.Records[fakeKey(payload)]
	if !ok {
		return fmt.Errorf("%w: no signature record for payload (fake)", ErrSignatureVerification)
	}
	re, err := CompileIdentityRegex(identityRegex)
	if err != nil {
		return err
	}
	if !re.MatchString(rec.Identity) {
		return fmt.Errorf("%w: identity %q does not match %q",
			ErrIdentityMismatch, rec.Identity, identityRegex)
	}
	if issuer != "" && rec.Issuer != issuer {
		return fmt.Errorf("%w: issuer %q does not match %q",
			ErrSignatureVerification, rec.Issuer, issuer)
	}
	return nil
}

// fakeKey is the per-payload key the FakeVerifier looks up. We use a
// short prefix of sha256 so test code can assert against a stable string;
// importing crypto/sha256 here keeps the FakeVerifier self-contained.
func fakeKey(payload []byte) string {
	return hashKeyPrefix(payload)
}

// hashKeyPrefix is exported via package-internal access so tests can
// register records with NewFakeRecord(payload).
func hashKeyPrefix(payload []byte) string {
	h := sha256Hex(payload)
	if len(h) < 16 {
		return h
	}
	return h[:16]
}

// NewFakeVerifier returns an empty FakeVerifier the test populates.
func NewFakeVerifier() *FakeVerifier {
	return &FakeVerifier{Records: map[string]FakeRecord{}}
}

// Allow registers a signature record for payload. Calling Allow twice
// with the same payload overwrites the prior record.
func (f *FakeVerifier) Allow(payload []byte, identity, issuer string) {
	f.Records[fakeKey(payload)] = FakeRecord{Identity: identity, Issuer: issuer}
}

// Compile-time check.
var _ CosignVerifier = (*sigstoreVerifier)(nil)
var _ CosignVerifier = (*FakeVerifier)(nil)
