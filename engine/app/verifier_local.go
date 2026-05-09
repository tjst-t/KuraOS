package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// LocalVerifier is an offline-first cosign-shaped CosignVerifier. It does NOT
// connect to Sigstore TUF / Fulcio / Rekor; instead it verifies bundles
// produced by the locally-managed signing key (the same key the operator
// generates with `kura app registry init`).
//
// This is the v1 production verifier. Sigstore-go full integration (TUF root
// + Rekor inclusion proof + Fulcio identity binding) is on the v1.x backlog;
// the LocalVerifier upholds the same fail-closed contract:
//   - bundle is required
//   - identity_regex must compile and match the bundle's recorded identity
//   - signature must verify against the payload bytes with the trusted key
//
// Bundle JSON shape (intentionally small):
//
//	{
//	  "key_id":   "<sha256 of public key>",
//	  "identity": "kuraos-local:<operator-name>",
//	  "issuer":   "kuraos-local",
//	  "sig":      "<base64 ed25519 signature of payload>"
//	}
//
// Trusted public keys live in /var/lib/kura/app-keys/<key_id>.pub (raw 32-byte
// ed25519 key, base64-encoded). cmd/kura wires the directory; tests inject
// keys directly via WithTrustedKey.
type LocalVerifier struct {
	mu      sync.RWMutex
	keys    map[string]ed25519.PublicKey
	keysDir string
}

// NewLocalVerifier returns an empty verifier. Caller adds keys via
// WithTrustedKey or sets KeysDir + LoadFromDir.
func NewLocalVerifier() *LocalVerifier {
	return &LocalVerifier{keys: map[string]ed25519.PublicKey{}}
}

// WithTrustedKey registers pub against keyID for verification. Returns the
// verifier so calls can chain. Used by tests + setup wizard.
func (v *LocalVerifier) WithTrustedKey(keyID string, pub ed25519.PublicKey) *LocalVerifier {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys[keyID] = pub
	return v
}

// SetKeysDir configures the directory LoadFromDir scans.
func (v *LocalVerifier) SetKeysDir(dir string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keysDir = dir
}

// LoadFromDir reads every *.pub file from KeysDir as base64-encoded ed25519
// public keys. The filename (without .pub) is the keyID.
func (v *LocalVerifier) LoadFromDir() error {
	v.mu.RLock()
	dir := v.keysDir
	v.mu.RUnlock()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("local verifier: read keys dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		raw, err := os.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return fmt.Errorf("local verifier: read %s: %w", e.Name(), err)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return fmt.Errorf("local verifier: bad key %s", e.Name())
		}
		keyID := strings.TrimSuffix(e.Name(), ".pub")
		v.WithTrustedKey(keyID, ed25519.PublicKey(decoded))
	}
	return nil
}

// VerifyBlob checks bundle against payload + identityRegex + issuer.
//
// Failure modes (each maps to ErrSignatureVerification or ErrIdentityMismatch
// so callers can distinguish identity vs signature bugs):
//
//	bundle missing                             -> ErrSignatureVerification
//	bundle JSON parse failure                  -> ErrSignatureVerification
//	identity_regex empty / unparseable          -> ErrSignatureVerification
//	bundle.identity does not match regex        -> ErrIdentityMismatch
//	bundle.issuer mismatch (when issuer set)    -> ErrSignatureVerification
//	key_id not in trusted set                   -> ErrSignatureVerification
//	signature verification failure              -> ErrSignatureVerification
func (v *LocalVerifier) VerifyBlob(_ context.Context, payload, bundle []byte, identityRegex, issuer string) error {
	if len(payload) == 0 || len(bundle) == 0 {
		return fmt.Errorf("%w: empty payload or bundle", ErrSignatureVerification)
	}
	if identityRegex == "" {
		return fmt.Errorf("%w: identity_regex is empty", ErrSignatureVerification)
	}
	re, err := CompileIdentityRegex(identityRegex)
	if err != nil {
		return err
	}
	var b localBundle
	if err := json.Unmarshal(bundle, &b); err != nil {
		return fmt.Errorf("%w: parse bundle: %v", ErrSignatureVerification, err)
	}
	if !re.MatchString(b.Identity) {
		return fmt.Errorf("%w: identity %q does not match %q",
			ErrIdentityMismatch, b.Identity, identityRegex)
	}
	if issuer != "" && b.Issuer != issuer {
		return fmt.Errorf("%w: issuer %q does not match %q",
			ErrSignatureVerification, b.Issuer, issuer)
	}
	v.mu.RLock()
	pub, ok := v.keys[b.KeyID]
	v.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: unknown key_id %s", ErrSignatureVerification, b.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(b.Sig)
	if err != nil {
		return fmt.Errorf("%w: decode sig: %v", ErrSignatureVerification, err)
	}
	digest := sha256.Sum256(payload)
	// We sign the SHA-256 of the payload (matches cosign's blob signing
	// convention). The verifier must hash too — never sign raw payloads
	// because ed25519 then becomes vulnerable to length-extension-style
	// substitution attacks via JSON/YAML re-encoding.
	if !ed25519.Verify(pub, digest[:], sig) {
		return fmt.Errorf("%w: ed25519 verify failed", ErrSignatureVerification)
	}
	return nil
}

// localBundle is the JSON shape the LocalVerifier consumes / produces.
type localBundle struct {
	KeyID    string `json:"key_id"`
	Identity string `json:"identity"`
	Issuer   string `json:"issuer"`
	Sig      string `json:"sig"`
}

// SignBlobLocal produces a bundle for payload using priv. keyID is the
// public key's identifier. identity is recorded verbatim so the verifier
// can match it against identity_regex.
//
// Used by:
//   - Tests (sign fixture bundles).
//   - cmd/kura's `kura app registry sign` (operator builds a local registry
//     and signs registry.json + manifest.yaml with their key).
func SignBlobLocal(payload []byte, priv ed25519.PrivateKey, keyID, identity, issuer string) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("local sign: empty payload")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("local sign: key size %d", len(priv))
	}
	digest := sha256.Sum256(payload)
	sig := ed25519.Sign(priv, digest[:])
	b := localBundle{
		KeyID:    keyID,
		Identity: identity,
		Issuer:   issuer,
		Sig:      base64.StdEncoding.EncodeToString(sig),
	}
	return json.Marshal(b)
}

// GenerateLocalKeypair returns a fresh ed25519 keypair and its keyID
// (sha256 prefix of the public key, hex). The operator stores the .key
// (private) under engine/system credential vault and the .pub under
// /var/lib/kura/app-keys/<keyID>.pub.
func GenerateLocalKeypair() (pub ed25519.PublicKey, priv ed25519.PrivateKey, keyID string, err error) {
	pub, priv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("ed25519 generate: %w", err)
	}
	sum := sha256.Sum256(pub)
	return pub, priv, hex.EncodeToString(sum[:8]), nil
}

var _ CosignVerifier = (*LocalVerifier)(nil)
