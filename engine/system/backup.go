package system

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// BackupSchemaVersion is bumped when the tarball layout changes
// incompatibly. v1 == case X (config.json + secrets.kura.age + manifest).
const BackupSchemaVersion = 1

// BackupManifest is the per-tarball metadata. Lets restore reject a wrong
// tarball without trying to age-decrypt blindly.
type BackupManifest struct {
	SchemaVersion  int       `json:"schema_version"`
	CreatedAt      time.Time `json:"created_at"`
	HostHint       string    `json:"host_hint,omitempty"`
	VaultEncrypted bool      `json:"vault_encrypted"`
}

// VaultDocument is the JSON shape of secrets.kura before age encryption.
// Versioned so restore can migrate from v1 -> v2 if we add fields.
type VaultDocument struct {
	SchemaVersion int          `json:"schema_version"`
	Credentials   []Credential `json:"credentials"`
	UIDAllocs     []UIDAlloc   `json:"uid_allocs"`
	GIDAllocs     []GIDAlloc   `json:"gid_allocs"`
}

// UIDAlloc / GIDAlloc are the persisted allocation rows. Round-trip
// preserves them so restored users land on the same uid/gid.
type UIDAlloc struct {
	UserID string `json:"user_id"`
	UID    int    `json:"uid"`
}

type GIDAlloc struct {
	GroupID string `json:"group_id"`
	GID     int    `json:"gid"`
}

// BackupOptions holds the inputs to WriteBackup.
type BackupOptions struct {
	ConfigJSON []byte
	Passphrase string // empty = ERROR (vault must be encrypted, case X)
	HostHint   string
}

// WriteBackup writes a tarball containing config.json (plaintext) and
// secrets.kura.age (age-encrypted vault) to dst.
//
// Layout:
//
//	manifest.json
//	config.json
//	secrets.kura.age
//
// Encryption is mandatory — case X requires vault to never be plaintext on
// disk. Passing an empty passphrase returns an error rather than writing
// a plaintext vault.
func WriteBackup(ctx context.Context, dst io.Writer, db *sql.DB, opts BackupOptions) error {
	if opts.Passphrase == "" {
		return errors.New("system: backup requires a passphrase (vault must be age-encrypted)")
	}

	creds, err := AllCredentials(ctx, db)
	if err != nil {
		return err
	}
	uids, err := loadUIDAllocs(ctx, db)
	if err != nil {
		return err
	}
	gids, err := loadGIDAllocs(ctx, db)
	if err != nil {
		return err
	}

	doc := VaultDocument{
		SchemaVersion: BackupSchemaVersion,
		Credentials:   creds,
		UIDAllocs:     uids,
		GIDAllocs:     gids,
	}
	plain, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("system: marshal vault: %w", err)
	}
	var enc bytes.Buffer
	if err := EncryptVault(&enc, plain, opts.Passphrase); err != nil {
		return err
	}

	manifest := BackupManifest{
		SchemaVersion:  BackupSchemaVersion,
		CreatedAt:      time.Now().UTC(),
		HostHint:       opts.HostHint,
		VaultEncrypted: true,
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("system: marshal manifest: %w", err)
	}

	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)

	for _, file := range []struct {
		name string
		data []byte
	}{
		{"manifest.json", manifestRaw},
		{"config.json", opts.ConfigJSON},
		{"secrets.kura.age", enc.Bytes()},
	} {
		hdr := &tar.Header{
			Name:    file.name,
			Mode:    0o600,
			Size:    int64(len(file.data)),
			ModTime: manifest.CreatedAt,
			Format:  tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("system: tar header %q: %w", file.name, err)
		}
		if _, err := tw.Write(file.data); err != nil {
			return fmt.Errorf("system: tar write %q: %w", file.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("system: tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("system: gzip close: %w", err)
	}
	return nil
}

// RestoreResult is what ReadBackup returns to the CLI.
type RestoreResult struct {
	Manifest   BackupManifest
	ConfigJSON []byte
	Vault      VaultDocument
}

// ReadBackup reads a tarball from src, age-decrypts the vault using
// passphrase, and returns the manifest + plaintext config.json + parsed
// vault contents. The CLI then merges the credentials back into state.db.
func ReadBackup(ctx context.Context, src io.Reader, passphrase string) (RestoreResult, error) {
	if passphrase == "" {
		return RestoreResult{}, errors.New("system: restore requires a passphrase (vault is age-encrypted)")
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("system: gzip open: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return RestoreResult{}, fmt.Errorf("system: tar next: %w", err)
		}
		buf := &bytes.Buffer{}
		if _, err := io.Copy(buf, tr); err != nil {
			return RestoreResult{}, fmt.Errorf("system: tar read %q: %w", hdr.Name, err)
		}
		files[hdr.Name] = buf.Bytes()
	}

	manifestRaw, ok := files["manifest.json"]
	if !ok {
		return RestoreResult{}, errors.New("system: backup missing manifest.json")
	}
	var manifest BackupManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return RestoreResult{}, fmt.Errorf("system: parse manifest: %w", err)
	}
	if manifest.SchemaVersion != BackupSchemaVersion {
		return RestoreResult{}, fmt.Errorf("system: unsupported backup schema %d (this kura speaks %d)",
			manifest.SchemaVersion, BackupSchemaVersion)
	}

	cfg, ok := files["config.json"]
	if !ok {
		return RestoreResult{}, errors.New("system: backup missing config.json")
	}
	enc, ok := files["secrets.kura.age"]
	if !ok {
		return RestoreResult{}, errors.New("system: backup missing secrets.kura.age")
	}

	plain, err := DecryptVault(bytes.NewReader(enc), passphrase)
	if err != nil {
		return RestoreResult{}, err
	}
	var vault VaultDocument
	if err := json.Unmarshal(plain, &vault); err != nil {
		return RestoreResult{}, fmt.Errorf("system: parse vault: %w", err)
	}

	return RestoreResult{Manifest: manifest, ConfigJSON: cfg, Vault: vault}, nil
}

// ApplyVaultRestore re-inserts the vault rows + uid/gid allocations into
// db. Used by `kura restore`. Existing rows are overwritten on conflict
// (kind, owner_kind, owner_id) so a partial state.db can be promoted to
// match the backup without manual cleanup.
func ApplyVaultRestore(ctx context.Context, db *sql.DB, vault VaultDocument) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("system: begin restore: %w", err)
	}
	defer tx.Rollback()

	for _, c := range vault.Credentials {
		if err := upsertCredential(ctx, tx, c); err != nil {
			return err
		}
	}
	for _, u := range vault.UIDAllocs {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO uid_alloc (user_id, uid) VALUES (?, ?)
			ON CONFLICT (user_id) DO UPDATE SET uid = excluded.uid
		`, u.UserID, u.UID)
		if err != nil {
			return fmt.Errorf("system: restore uid_alloc: %w", err)
		}
	}
	for _, g := range vault.GIDAllocs {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO gid_alloc (group_id, gid) VALUES (?, ?)
			ON CONFLICT (group_id) DO UPDATE SET gid = excluded.gid
		`, g.GroupID, g.GID)
		if err != nil {
			return fmt.Errorf("system: restore gid_alloc: %w", err)
		}
	}
	return tx.Commit()
}

func loadUIDAllocs(ctx context.Context, db *sql.DB) ([]UIDAlloc, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id, uid FROM uid_alloc ORDER BY user_id`)
	if err != nil {
		return nil, fmt.Errorf("system: load uid_alloc: %w", err)
	}
	defer rows.Close()
	var out []UIDAlloc
	for rows.Next() {
		var u UIDAlloc
		if err := rows.Scan(&u.UserID, &u.UID); err != nil {
			return nil, fmt.Errorf("system: scan uid_alloc: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func loadGIDAllocs(ctx context.Context, db *sql.DB) ([]GIDAlloc, error) {
	rows, err := db.QueryContext(ctx, `SELECT group_id, gid FROM gid_alloc ORDER BY group_id`)
	if err != nil {
		return nil, fmt.Errorf("system: load gid_alloc: %w", err)
	}
	defer rows.Close()
	var out []GIDAlloc
	for rows.Next() {
		var g GIDAlloc
		if err := rows.Scan(&g.GroupID, &g.GID); err != nil {
			return nil, fmt.Errorf("system: scan gid_alloc: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
