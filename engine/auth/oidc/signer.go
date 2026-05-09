package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SigningAlg is the JWS algorithm we use for ID tokens. RS256 is the
// universally-supported default for OIDC RPs.
const SigningAlg = "RS256"

// SigningKey holds an RSA-2048 private/public pair plus the kid the JWKS
// publishes. Loaded from the credential vault; generated on first run.
type SigningKey struct {
	KID     string
	Private *rsa.PrivateKey
	Public  *rsa.PublicKey
}

// CredentialStore is the slice of engine/system the OIDC signer needs.
// Defining it as an interface here (consumer-side, DESIGN_PRINCIPLES
// priority #9) keeps engine/auth/oidc decoupled from engine/system's
// full surface.
type CredentialStore interface {
	LookupCredential(ctx context.Context, kind, ownerKind, ownerID string) (string, error)
	SetCredential(ctx context.Context, kind, ownerKind, ownerID, value string) error
}

// EnsureSigningKey returns the active OIDC signing key, generating a new
// RSA-2048 pair and persisting it to the vault on first call. Subsequent
// calls retrieve the same key so id_tokens issued before a restart
// remain verifiable after.
func EnsureSigningKey(ctx context.Context, cs CredentialStore) (*SigningKey, error) {
	if cs == nil {
		return nil, errors.New("oidc: credential store is nil")
	}
	const kind = "oidc_signing_key"
	const ownerKind = "system"
	const ownerID = "active"

	pem, err := cs.LookupCredential(ctx, kind, ownerKind, ownerID)
	if err == nil && pem != "" {
		return parseSigningKeyPEM(pem)
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("oidc: generate rsa: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("oidc: marshal pkcs8: %w", err)
	}
	encoded := encodePEM("PRIVATE KEY", der)
	if err := cs.SetCredential(ctx, kind, ownerKind, ownerID, encoded); err != nil {
		return nil, fmt.Errorf("oidc: persist key: %w", err)
	}
	return &SigningKey{
		KID:     deriveKID(&priv.PublicKey),
		Private: priv,
		Public:  &priv.PublicKey,
	}, nil
}

func parseSigningKeyPEM(encoded string) (*SigningKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("oidc: pem decode: empty block")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Older keys may have been written as PKCS1.
		priv, perr := x509.ParsePKCS1PrivateKey(block.Bytes)
		if perr != nil {
			return nil, fmt.Errorf("oidc: parse private: %w (also pkcs1: %v)", err, perr)
		}
		return &SigningKey{KID: deriveKID(&priv.PublicKey), Private: priv, Public: &priv.PublicKey}, nil
	}
	priv, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("oidc: vault key is %T, want *rsa.PrivateKey", keyAny)
	}
	return &SigningKey{KID: deriveKID(&priv.PublicKey), Private: priv, Public: &priv.PublicKey}, nil
}

func encodePEM(typeStr string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: typeStr, Bytes: der}))
}

// deriveKID derives a stable key identifier from the public key bytes.
// SHA256(SubjectPublicKeyInfo) truncated to 8 bytes hex — short enough
// to read in the JWKS, long enough to uniquely identify rotated keys.
func deriveKID(pub *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		// MarshalPKIXPublicKey only fails on unsupported key types;
		// RSA is supported, so this branch is truly unreachable.
		return "unknown"
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:8])
}

// SignIDToken builds a compact JWS over claims and returns the
// "header.payload.signature" string.
func (k *SigningKey) SignIDToken(claims map[string]any) (string, error) {
	header := map[string]string{"alg": SigningAlg, "typ": "JWT", "kid": k.KID}
	headerJSON, _ := json.Marshal(header)
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("oidc: marshal claims: %w", err)
	}
	signingInput := base64URLEncode(headerJSON) + "." + base64URLEncode(payloadJSON)
	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.Private, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("oidc: sign: %w", err)
	}
	return signingInput + "." + base64URLEncode(sig), nil
}

// VerifyIDToken verifies a JWS-signed token against k.Public and returns
// the decoded claims. Used by tests; production RPs use their own JOSE
// libs.
func (k *SigningKey) VerifyIDToken(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidc: token does not have 3 segments")
	}
	headerBytes, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("oidc: header: %w", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("oidc: header json: %w", err)
	}
	if header["alg"] != SigningAlg {
		return nil, fmt.Errorf("oidc: alg %q != %q", header["alg"], SigningAlg)
	}
	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("oidc: payload: %w", err)
	}
	sig, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidc: sig: %w", err)
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(k.Public, crypto.SHA256, hash[:], sig); err != nil {
		return nil, fmt.Errorf("oidc: verify: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("oidc: claims json: %w", err)
	}
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().UTC().After(time.Unix(int64(exp), 0)) {
			return nil, errors.New("oidc: token expired")
		}
	}
	return claims, nil
}

// JWKS returns the JSON Web Key Set publishing k.Public so RPs can
// validate id_tokens without contacting /userinfo.
func (k *SigningKey) JWKS() map[string]any {
	return map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": SigningAlg,
				"kid": k.KID,
				"n":   base64URLEncode(k.Public.N.Bytes()),
				"e":   base64URLEncode(intToBytes(k.Public.E)),
			},
		},
	}
}

func base64URLEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func intToBytes(n int) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
	for i, x := range b {
		if x != 0 {
			return b[i:]
		}
	}
	return []byte{0}
}
