package user

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2idParams names the cost knobs of the argon2id KDF. Defaults below
// follow OWASP Password Storage Cheat Sheet (2024 revision):
//
//	t=2, m=19MiB, p=1
//
// These are the minimum recommended for argon2id with 32-byte salt + 32-byte
// output and are achievable on the lowest-end NAS hardware (raspi-class) in
// well under a second. Calling code can override per environment.
type Argon2idParams struct {
	Time    uint32 // iterations
	Memory  uint32 // KiB
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultArgon2idParams is the parameter set used by NewHasher() when no
// override is supplied.
//
// Source: https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#argon2id
var DefaultArgon2idParams = Argon2idParams{
	Time:    2,
	Memory:  19 * 1024,
	Threads: 1,
	SaltLen: 16,
	KeyLen:  32,
}

// Hasher is the password hash interface every caller depends on. The store
// holds a Hasher rather than calling argon2 directly so tests can inject a
// fast deterministic fake — DESIGN_PRINCIPLES priority #9 (テスタビリティ).
type Hasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (bool, error)
}

// ErrInvalidHash is returned when an encoded verifier cannot be parsed.
// Callers should treat it as "not the right shape" rather than "wrong
// password" — the latter is a successful Verify that returns (false, nil).
var ErrInvalidHash = errors.New("invalid argon2id verifier")

type argon2idHasher struct {
	params Argon2idParams
	rng    func([]byte) (int, error)
}

// NewHasher returns the production argon2id Hasher with DefaultArgon2idParams.
func NewHasher() Hasher { return NewHasherWith(DefaultArgon2idParams) }

// NewHasherWith returns an argon2id Hasher with explicit cost params. Useful
// for tests (lower memory) and for ops to dial up cost on faster hardware.
func NewHasherWith(p Argon2idParams) Hasher {
	return &argon2idHasher{params: p, rng: rand.Read}
}

// Hash produces a PHC-encoded argon2id verifier:
//
//	$argon2id$v=19$m=<mem>,t=<time>,p=<threads>$<salt-b64>$<hash-b64>
//
// The encoding is portable and self-describing so verifying does not need
// out-of-band knowledge of the params used at hash time.
func (h *argon2idHasher) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is empty")
	}
	salt := make([]byte, h.params.SaltLen)
	if _, err := h.rng(salt); err != nil {
		return "", fmt.Errorf("argon2id: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.Memory, h.params.Threads, h.params.KeyLen)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.Memory,
		h.params.Time,
		h.params.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// Verify recomputes the hash with the params + salt embedded in encoded and
// constant-time compares it against the stored key. (false, nil) means the
// password is wrong but the verifier was well-formed. (false, err) means the
// verifier itself is malformed.
func (h *argon2idHasher) Verify(password, encoded string) (bool, error) {
	p, salt, key, err := decodeArgon2id(encoded)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	if subtle.ConstantTimeCompare(candidate, key) == 1 {
		return true, nil
	}
	return false, nil
}

func decodeArgon2id(encoded string) (Argon2idParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// parts[0] is empty (string starts with "$"), so a valid encoding has 6
	// fields: "", "argon2id", "v=...", "m=,t=,p=", "<salt>", "<hash>".
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Argon2idParams{}, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Argon2idParams{}, nil, nil, ErrInvalidHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidHash
	}
	return Argon2idParams{
		Memory:  memory,
		Time:    time,
		Threads: threads,
		SaltLen: uint32(len(salt)),
		KeyLen:  uint32(len(key)),
	}, salt, key, nil
}
