package fileapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// TokenClaims are the decoded fields from a valid X-Kura-Token.
type TokenClaims struct {
	AppName   string
	UserID    string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// TokenValidator verifies an X-Kura-Token header value.
// The Gateway issues tokens; the File API validates them.
// Production uses HMACValidator (key from vault); tests use FixedKeyValidator.
type TokenValidator interface {
	// Validate parses and validates the token, returning the claims on success.
	Validate(ctx context.Context, token string) (TokenClaims, error)
}

// HMACValidator validates HMAC-SHA256 signed tokens.
// Token format: "<app>.<user>.<iat>.<exp>.<sig>"
// where sig = HMAC-SHA256(key, "<app>.<user>.<iat>.<exp>")
type HMACValidator struct {
	Key []byte
}

// Validate implements TokenValidator.
func (v *HMACValidator) Validate(_ context.Context, token string) (TokenClaims, error) {
	parts := strings.SplitN(token, ".", 5)
	if len(parts) != 5 {
		return TokenClaims{}, fmt.Errorf("fileapi: invalid token format")
	}
	appName, userID, iatStr, expStr, sig := parts[0], parts[1], parts[2], parts[3], parts[4]

	// Verify signature.
	payload := strings.Join(parts[:4], ".")
	expected := hmacSig(v.Key, payload)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return TokenClaims{}, fmt.Errorf("fileapi: token signature invalid")
	}

	// Parse timestamps.
	iat, err := parseUnix(iatStr)
	if err != nil {
		return TokenClaims{}, fmt.Errorf("fileapi: token iat invalid: %w", err)
	}
	exp, err := parseUnix(expStr)
	if err != nil {
		return TokenClaims{}, fmt.Errorf("fileapi: token exp invalid: %w", err)
	}
	if time.Now().After(exp) {
		return TokenClaims{}, fmt.Errorf("fileapi: token expired")
	}

	return TokenClaims{
		AppName:   appName,
		UserID:    userID,
		IssuedAt:  iat,
		ExpiresAt: exp,
	}, nil
}

// IssueToken creates a signed token for the given app and user. Used by the
// gateway when an app makes a pre-flight request for a file scope.
func IssueToken(key []byte, appName, userID string, ttl time.Duration) string {
	iat := time.Now()
	exp := iat.Add(ttl)
	payload := fmt.Sprintf("%s.%s.%d.%d", appName, userID, iat.Unix(), exp.Unix())
	sig := hmacSig(key, payload)
	return payload + "." + sig
}

func hmacSig(key []byte, payload string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func parseUnix(s string) (time.Time, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(n, 0), nil
}

// NoopValidator is a token validator that accepts any non-empty token.
// Use ONLY in tests and in the built-in filebrowser (which uses session auth instead).
type NoopValidator struct{}

func (NoopValidator) Validate(_ context.Context, token string) (TokenClaims, error) {
	if token == "" {
		return TokenClaims{}, fmt.Errorf("fileapi: token missing")
	}
	return TokenClaims{AppName: "test", UserID: "admin"}, nil
}
