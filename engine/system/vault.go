package system

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf16"

	"filippo.io/age"
	"golang.org/x/crypto/md4"
)

func realSmbNow() int64 { return time.Now().Unix() }

// NTHash returns the SMB / NTLM password hash for plaintext: MD4 of the
// UTF-16LE encoding. The MD4 cipher is broken in every adversarial sense
// but it is what the SMB protocol mandates on the server side — this
// function is not a security primitive, it is a protocol mirror.
//
//nolint:staticcheck // MD4 is required by the SMB protocol contract.
func NTHash(plaintext string) string {
	utf16Pts := utf16.Encode([]rune(plaintext))
	buf := make([]byte, 0, 2*len(utf16Pts))
	for _, r := range utf16Pts {
		buf = append(buf, byte(r), byte(r>>8))
	}
	h := md4.New()
	_, _ = h.Write(buf)
	sum := h.Sum(nil)
	return strings.ToUpper(hex.EncodeToString(sum))
}

// SmbPasswdLine builds the colon-delimited single-line credential format
// that smbpasswd / pdbedit accept on stdin:
//
//	username:uid:LANMAN:NTHASH:[U          ]:LCT-<HEX>:
//
// The LANMAN field is set to all-X (disabled) — modern SMB has not used
// LANMAN since SMB1 deprecation. Account flags [U] = normal user account.
// LCT-<HEX> is the Last-Changed-Timestamp in hex Unix epoch seconds —
// LCT-00000000 makes Samba treat the account as 'must change password
// at next login' (NT_STATUS_PASSWORD_MUST_CHANGE), so we use the
// current time.
func SmbPasswdLine(username string, uid int, ntHash string) string {
	return smbPasswdLineAt(username, uid, ntHash, smbNow())
}

func smbPasswdLineAt(username string, uid int, ntHash string, now int64) string {
	disabledLanman := strings.Repeat("X", 32)
	return fmt.Sprintf("%s:%d:%s:%s:[U          ]:LCT-%08X:",
		username, uid, disabledLanman, ntHash, now)
}

// smbNow returns the current Unix epoch seconds. Test code overrides
// this with a constant so SmbPasswdLine output is deterministic.
var smbNow = realSmbNow

// upsertCredential writes (kind, ownerKind, ownerID, value) into the vault.
// On conflict (same triple) the value is replaced and updated_at bumped.
func upsertCredential(ctx context.Context, tx *sql.Tx, c Credential) error {
	if c.Value == "" {
		return ErrCredentialEmpty
	}
	if c.ID == "" {
		c.ID = newOpaqueID()
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO credentials (id, kind, owner_kind, owner_id, value)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (kind, owner_kind, owner_id) DO UPDATE SET
		    value = excluded.value,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, c.ID, string(c.Kind), string(c.OwnerKind), c.OwnerID, c.Value)
	if err != nil {
		return fmt.Errorf("system: upsert credential: %w", err)
	}
	return nil
}

func lookupCredential(ctx context.Context, db *sql.DB, kind CredentialKind, ownerKind CredentialOwnerKind, ownerID string) (Credential, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, kind, owner_kind, owner_id, value
		FROM credentials WHERE kind = ? AND owner_kind = ? AND owner_id = ?
	`, string(kind), string(ownerKind), ownerID)
	var c Credential
	var k, ok string
	if err := row.Scan(&c.ID, &k, &ok, &c.OwnerID, &c.Value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Credential{}, ErrUserNotFound
		}
		return Credential{}, fmt.Errorf("system: lookup credential: %w", err)
	}
	c.Kind = CredentialKind(k)
	c.OwnerKind = CredentialOwnerKind(ok)
	return c, nil
}

// listCredentials returns all rows, ordered by kind for deterministic
// backup output. Used by backup/export only — runtime path uses
// lookupCredential by tuple.
func listCredentials(ctx context.Context, db *sql.DB) ([]Credential, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, owner_kind, owner_id, value
		FROM credentials ORDER BY kind, owner_kind, owner_id
	`)
	if err != nil {
		return nil, fmt.Errorf("system: list credentials: %w", err)
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		var k, ok string
		if err := rows.Scan(&c.ID, &k, &ok, &c.OwnerID, &c.Value); err != nil {
			return nil, fmt.Errorf("system: scan credential: %w", err)
		}
		c.Kind = CredentialKind(k)
		c.OwnerKind = CredentialOwnerKind(ok)
		out = append(out, c)
	}
	return out, rows.Err()
}

// EncryptVault writes plain to dst, age-encrypted with passphrase. age's
// scrypt recipient is the passphrase form chosen by DESIGN_PRINCIPLES
// priority #11; recipient (X25519 keypair) mode is deferred to v1.x.
func EncryptVault(dst io.Writer, plain []byte, passphrase string) error {
	if passphrase == "" {
		return errors.New("system: passphrase is empty")
	}
	rec, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return fmt.Errorf("system: age recipient: %w", err)
	}
	w, err := age.Encrypt(dst, rec)
	if err != nil {
		return fmt.Errorf("system: age encrypt: %w", err)
	}
	if _, err := w.Write(plain); err != nil {
		return fmt.Errorf("system: age write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("system: age close: %w", err)
	}
	return nil
}

// DecryptVault reads an age-encrypted stream from src and returns the
// plaintext. Wrong passphrase surfaces as a generic age error; callers
// translate to a friendly UI message.
func DecryptVault(src io.Reader, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("system: passphrase is empty")
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("system: age identity: %w", err)
	}
	r, err := age.Decrypt(src, id)
	if err != nil {
		return nil, fmt.Errorf("system: age decrypt: %w", err)
	}
	return io.ReadAll(r)
}

func newOpaqueID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("system: rand.Read: " + err.Error())
	}
	return hex.EncodeToString(b)
}
