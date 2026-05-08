// Package config defines the SSOT (config.json) schema for KuraOS and the
// import / export / diff / apply primitives that operate on it.
//
// VISION + DESIGN_PRINCIPLES priority #1: SQLite is a runtime cache; every
// observable state must round-trip through this struct without loss. Fields
// added in later sprints attach engine-specific sub-structs to the
// already-named sections below — they are never created at the top level
// without a corresponding field here.
//
// All sections use `omitempty` so an empty SQLite produces an empty section
// rather than {} clutter. An empty Config marshals to a minimal envelope
// that re-parses to itself, satisfying the round-trip invariant on day one.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Version is the schema version of config.json. Bumped when a backwards
// incompatible field rename / removal happens. Engines may reject documents
// they don't understand by checking this value.
const Version = 1

// Config is the top-level config.json document. Empty pointer fields keep
// the JSON output minimal until an engine sprint populates them.
type Config struct {
	SchemaVersion int `json:"schema_version"`

	// System covers cluster-wide settings that don't belong to any single
	// engine: hostname, locale, timezone, theme defaults.
	System *SystemConfig `json:"system,omitempty"`

	// Storage owns ZFS pools, datasets, snapshots, scrub schedules.
	Storage *StorageConfig `json:"storage,omitempty"`

	// Shares holds SMB / NFS share definitions. Engine sprint replaces
	// this with concrete fields.
	Shares *SharesConfig `json:"shares,omitempty"`

	// Users / groups / auth methods (local + OIDC federation).
	Users *UsersConfig `json:"users,omitempty"`

	// Network covers TLS, ACME providers, external access (tailscale).
	Network *NetworkConfig `json:"network,omitempty"`

	// Apps holds installed applications and their per-instance settings.
	Apps *AppsConfig `json:"apps,omitempty"`

	// Auth covers session / JWT secrets stored as env-var references only.
	Auth *AuthConfig `json:"auth,omitempty"`

	// Backup holds backup destinations and policies.
	Backup *BackupConfig `json:"backup,omitempty"`

	// Notifications holds channel definitions (email / webhook / etc).
	Notifications *NotificationsConfig `json:"notifications,omitempty"`

	// Monitor holds metric-collection knobs (retention, ring buffer size).
	Monitor *MonitorConfig `json:"monitor,omitempty"`

	// Logging holds JSONL retention settings.
	Logging *LoggingConfig `json:"logging,omitempty"`
}

// Sub-section structs. Fields are populated in subsequent sprints — the
// presence of the type ensures Engines can register an ApplyAdapter against
// a stable target.

type SystemConfig struct{}

// StorageConfig is the declarative shape of "storage" in config.json.
// Mirrors design.md §3.1: pools[], volumes[], snapshots[].
//
// Fields use simple string types for vdev layout / preset because config.json
// is the authoritative SSOT — engines validate values when applying. Keeping
// JSON loose here lets us add fields (e.g. dedup once supported) without a
// breaking schema bump.
type StorageConfig struct {
	Pools   []PoolEntry   `json:"pools,omitempty"`
	Volumes []VolumeEntry `json:"volumes,omitempty"`
}

// PoolEntry mirrors the one entry of `pools[]` in design.md §3.1. `mode`
// distinguishes create-from-scratch (default, omitted) from importing an
// existing pool ("import"). The latter triggers `zpool import` instead of
// `zpool create`.
type PoolEntry struct {
	Name                string         `json:"name"`
	Mode                string         `json:"mode,omitempty"` // "" | "create" | "import"
	Topology            *TopologyEntry `json:"topology,omitempty"`
	Special             *TopologyEntry `json:"special,omitempty"`
	SmallBlockThreshold string         `json:"small_block_threshold,omitempty"`
	Spares              []string       `json:"spares,omitempty"`
	AutoShare           bool           `json:"auto_share,omitempty"`
}

// TopologyEntry maps to the design.md `topology` sub-object and `special`.
type TopologyEntry struct {
	Type  string   `json:"type"`
	Disks []string `json:"disks"`
}

// VolumeEntry mirrors the one entry of `volumes[]` in design.md §3.1. The
// `quota` field is a human string ("500G", "2T"); engines parse it when
// applying.
type VolumeEntry struct {
	Name   string `json:"name"`
	Quota  string `json:"quota,omitempty"`
	Preset string `json:"preset,omitempty"`
}

type SharesConfig struct{}
type UsersConfig struct{}
type NetworkConfig struct{}
type AppsConfig struct{}
type AuthConfig struct{}
type BackupConfig struct{}
type NotificationsConfig struct{}
type MonitorConfig struct{}
type LoggingConfig struct{}

// New returns an empty Config with SchemaVersion set. Always use New rather
// than `&Config{}` so the version field is never accidentally zero.
func New() *Config { return &Config{SchemaVersion: Version} }

// Marshal writes the canonical JSON form (indented, sorted keys) of cfg.
// Used by `kura config export` and round-trip tests. The trailing newline
// matches the convention of other dotfile-style configs (.editorconfig,
// .prettierrc) so VCS diffs stay clean.
func Marshal(cfg *Config) ([]byte, error) {
	if cfg == nil {
		cfg = New()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, fmt.Errorf("config: marshal: %w", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal parses raw JSON into a Config. Unknown top-level keys are
// rejected (DESIGN_PRINCIPLES: 明示的 > 暗黙的) so a typo in a hand-written
// config.json fails loudly instead of being silently dropped during apply.
func Unmarshal(raw []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	cfg := New()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	if cfg.SchemaVersion == 0 {
		// Tolerate documents authored before schema_version existed by
		// stamping the current version. Future versions can require an
		// explicit value once we have something to migrate against.
		cfg.SchemaVersion = Version
	}
	return cfg, nil
}

// ReadFrom is a streaming Unmarshal helper used by CLI/HTTP entry points
// that already have an io.Reader on hand.
func ReadFrom(r io.Reader) (*Config, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("config: read: %w", err)
	}
	return Unmarshal(raw)
}
