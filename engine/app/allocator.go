package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	"github.com/kuraos-org/kura/engine/system"
)

// PortRange constrains where the allocator picks host ports. The default
// 49152-65535 is IANA's dynamic / private range — same range Linux uses
// for ephemeral source ports, but we use it for stable bindings instead.
// Operator can shrink it via PortAllocator.Range to reserve a band.
type PortRange struct {
	Min int
	Max int
}

// DefaultPortRange is the allocator default. Picked to avoid clashing
// with well-known service ports operators tend to bind manually
// (postgres 5432, redis 6379, ...).
var DefaultPortRange = PortRange{Min: 49152, Max: 65535}

// PortReservation is one row of app_port_reservations. AC-S1bccf5-3-2
// requires the (AppID, Container, ManifestPort) tuple to keep returning
// the same HostPort across restarts.
type PortReservation struct {
	AppID        string `json:"app_id"`
	Container    string `json:"container"`
	ManifestPort int    `json:"manifest_port"`
	HostPort     int    `json:"host_port"`
}

// PortAllocator hands out host ports for app containers. Backed by the
// app_port_reservations table — once allocated, the binding is stable.
type PortAllocator struct {
	DB    *sql.DB
	Range PortRange
}

// NewPortAllocator returns an allocator with DefaultPortRange.
func NewPortAllocator(db *sql.DB) *PortAllocator {
	return &PortAllocator{DB: db, Range: DefaultPortRange}
}

// Reserve returns the host port for (appID, container, manifestPort).
// First call inserts a new reservation; subsequent calls return the
// previously-allocated host_port verbatim. AC-S1bccf5-3-2 (再起動を跨い
// で保持される) is satisfied by the SQLite primary key + unique index on
// host_port.
func (a *PortAllocator) Reserve(ctx context.Context, r PortReservation) (int, error) {
	if a.DB == nil {
		return 0, errors.New("app: allocator DB is nil")
	}
	if r.AppID == "" || r.Container == "" || r.ManifestPort <= 0 {
		return 0, fmt.Errorf("app: invalid reservation: %+v", r)
	}
	rng := a.Range
	if rng.Min == 0 && rng.Max == 0 {
		rng = DefaultPortRange
	}
	if rng.Min >= rng.Max {
		return 0, fmt.Errorf("app: invalid port range %v", rng)
	}

	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("app: begin: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT host_port FROM app_port_reservations
		WHERE app_id = ? AND container = ? AND manifest_port = ?
	`, r.AppID, r.Container, r.ManifestPort)
	var existing int
	if err := row.Scan(&existing); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("app: lookup reservation: %w", err)
	}

	host, err := pickFreePort(ctx, tx, rng)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO app_port_reservations
		    (app_id, container, manifest_port, host_port)
		VALUES (?, ?, ?, ?)
	`, r.AppID, r.Container, r.ManifestPort, host)
	if err != nil {
		return 0, fmt.Errorf("app: insert reservation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("app: commit: %w", err)
	}
	return host, nil
}

// LookupReservations returns every (container, manifest_port, host_port)
// row for appID. Used by config.json export so the structural state can
// reproduce the same compose YAML.
func (a *PortAllocator) LookupReservations(ctx context.Context, appID string) ([]PortReservation, error) {
	rows, err := a.DB.QueryContext(ctx, `
		SELECT app_id, container, manifest_port, host_port
		FROM app_port_reservations WHERE app_id = ?
		ORDER BY container, manifest_port
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("app: list reservations: %w", err)
	}
	defer rows.Close()
	var out []PortReservation
	for rows.Next() {
		var r PortReservation
		if err := rows.Scan(&r.AppID, &r.Container, &r.ManifestPort, &r.HostPort); err != nil {
			return nil, fmt.Errorf("app: scan reservation: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// pickFreePort scans the configured range and returns the first port not
// already reserved. We start from rng.Min and walk forward — predictable
// allocation order makes diffs in compose YAML stable as new apps land.
func pickFreePort(ctx context.Context, tx *sql.Tx, rng PortRange) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT host_port FROM app_port_reservations
		WHERE host_port BETWEEN ? AND ?
		ORDER BY host_port
	`, rng.Min, rng.Max)
	if err != nil {
		return 0, fmt.Errorf("app: scan reserved ports: %w", err)
	}
	defer rows.Close()
	taken := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return 0, fmt.Errorf("app: scan port: %w", err)
		}
		taken[p] = true
	}
	for p := rng.Min; p <= rng.Max; p++ {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("app: port range %d-%d exhausted", rng.Min, rng.Max)
}

// SecretStore writes app-internal secrets (DB passwords, OIDC client
// secrets) into the unified credential vault from Ssys001. Per
// DESIGN_PRINCIPLES priority #1 + forbidden, app secrets MUST live in
// the vault, NEVER in config.json or env files.
type SecretStore struct {
	System SystemEngine
}

// SystemEngine is the slice of engine/system.Engine the SecretStore
// needs. Declared here (consumer-side interface, Go duck-typing) so
// tests inject a fake without importing the production engine.
type SystemEngine interface {
	SetCredential(ctx context.Context, c system.Credential) error
	LookupCredential(ctx context.Context, kind system.CredentialKind, owner system.CredentialOwnerKind, ownerID string) (system.Credential, error)
}

// NewSecretStore wires the SecretStore against an engine/system.Engine.
func NewSecretStore(sys SystemEngine) *SecretStore {
	return &SecretStore{System: sys}
}

// EnsureSecret returns the existing vault secret for (appID, settingKey)
// or generates a fresh one and stores it. The returned plaintext is
// suitable for substitution into compose env (e.g. ${db_password}). Repeat
// calls return the same value — durability across reboots comes from the
// vault being SQLite-backed.
//
// settingKey is the manifest setting key (e.g. "db_password"); it is
// concatenated with appID to form the vault row's owner_id, so two apps
// with the same setting name keep their secrets independent.
func (s *SecretStore) EnsureSecret(ctx context.Context, appID, settingKey string) (string, error) {
	if s.System == nil {
		return "", errors.New("app: SecretStore.System is nil")
	}
	if appID == "" || settingKey == "" {
		return "", fmt.Errorf("app: appID and settingKey required")
	}
	ownerID := appOwnerID(appID, settingKey)
	cred, err := s.System.LookupCredential(ctx, system.CredentialAppDBPassword, system.OwnerApp, ownerID)
	if err == nil && cred.Value != "" {
		return cred.Value, nil
	}
	if err != nil && !errors.Is(err, system.ErrUserNotFound) {
		return "", fmt.Errorf("app: lookup secret: %w", err)
	}
	secret, err := generateSecret(32)
	if err != nil {
		return "", err
	}
	if err := s.System.SetCredential(ctx, system.Credential{
		Kind:      system.CredentialAppDBPassword,
		OwnerKind: system.OwnerApp,
		OwnerID:   ownerID,
		Value:     secret,
	}); err != nil {
		return "", fmt.Errorf("app: store secret: %w", err)
	}
	return secret, nil
}

// appOwnerID is the deterministic vault owner_id for (appID, settingKey).
// Format: "<appID>:<settingKey>" — colon is illegal in app IDs and
// setting keys so the join is unambiguous.
func appOwnerID(appID, settingKey string) string {
	return appID + ":" + settingKey
}

// generateSecret produces nBytes of crypto-random material as a
// base32-no-padding string. base32 keeps the secret URL-safe and
// shell-safe (no /, +, =) so compose env interpolation never needs
// escaping.
func generateSecret(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", fmt.Errorf("app: secret length must be > 0")
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("app: rand.Read: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return strings.ToLower(enc.EncodeToString(buf)), nil
}
