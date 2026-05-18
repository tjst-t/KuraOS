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

// SharesConfig is the declarative shape of "shares" in config.json. Mirrors
// design.md §5: Shares 配列 (id/name/path/preset/access_mode/acl/protocol).
//
// Performance / compat tunables (server multi channel support, vfs objects,
// fruit:metadata, ...) are NOT in this struct. They are derived internally
// from `preset` (DESIGN_PRINCIPLES priority #2: 賢いデフォルト > 設定項目を
// 増やす). Anything tunable in smb.conf that isn't here means we have made a
// deliberate choice not to expose it.
type SharesConfig struct {
	Shares []ShareEntry `json:"shares,omitempty"`
}

// ShareEntry is one row of `shares[]`. ID is preserved across export → import
// so identity stays stable. ACL grants are namespaced by principal kind so
// future OIDC group sync remains schema-compatible.
type ShareEntry struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Protocol    string          `json:"protocol"`
	Preset      string          `json:"preset"`
	AccessMode  string          `json:"access_mode"`
	Description string          `json:"description,omitempty"`
	Disabled    bool            `json:"disabled,omitempty"`
	ACL         []ShareACLEntry `json:"acl,omitempty"`
}

// ShareACLEntry mirrors share.ACLEntry. Strings (not enums) keep config.json
// hand-editable; the engine validates on apply.
type ShareACLEntry struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// UsersConfig is the declarative shape of "users" in config.json. Per
// DESIGN_PRINCIPLES priority #1, the export contains structural fields
// only — every credential is replaced by `credential_state: "set"|"unset"`
// placeholder so a stolen config.json can never reproduce login.
type UsersConfig struct {
	Users []UserEntry `json:"users,omitempty"`
}

// UserEntry mirrors engine/user.User minus internal IDs / timestamps.
// CredentialState is "set" iff the user has at least one credential row in
// the vault (priority #1 — config.json never carries the secret itself).
type UserEntry struct {
	Username        string `json:"username"`
	DisplayName     string `json:"display_name,omitempty"`
	Role            string `json:"role"`
	Disabled        bool   `json:"disabled,omitempty"`
	CredentialState string `json:"credential_state"`
}
type NetworkConfig struct{}

// AppsConfig is the declarative shape of "apps" in config.json. v1
// declares the trusted app registries (design.md §7.9) and the list of
// installed app instances. Per DESIGN_PRINCIPLES priority #1, internal
// app secrets (DB password, OIDC client secret) NEVER appear here — they
// live in the credential vault and config.json carries only the
// CredentialState placeholder.
type AppsConfig struct {
	Registries []AppRegistryEntry `json:"registries,omitempty"`
	Installed  []AppInstanceEntry `json:"installed,omitempty"`
	// PortRange optionally constrains the host-side port allocator. Empty
	// fields fall back to the IANA dynamic range 49152-65535. Operators
	// expose this so they can reserve a band for non-KuraOS services.
	PortRange *PortRangeConfig `json:"port_range,omitempty"`
}

// PortRangeConfig is the operator-tunable port band used by the App engine's
// PortAllocator. Min is inclusive; Max is inclusive. v1 ignores values
// outside the IANA dynamic range.
type PortRangeConfig struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// AppRegistryEntry mirrors design.md §7.9 app_registries[] block.
// Identity_regex and issuer feed the cosign keyless verifier; trust.type
// must be "cosign_keyless" for v1 (any other value is rejected at load).
type AppRegistryEntry struct {
	Name  string                `json:"name"`
	URL   string                `json:"url"`
	Trust AppRegistryTrustEntry `json:"trust"`
}

// AppRegistryTrustEntry is the trust block of one registry.
type AppRegistryTrustEntry struct {
	Type          string `json:"type"`
	IdentityRegex string `json:"identity_regex"`
	Issuer        string `json:"issuer,omitempty"`
}

// AppInstanceEntry is one row of installed apps. Settings carry only
// non-secret values; secrets are flagged with credential_state placeholder
// (priority #1) so a stolen config.json cannot reproduce login.
type AppInstanceEntry struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Registry      string            `json:"registry"`
	SettingsState []AppSettingState `json:"settings_state,omitempty"`
}

// AppSettingState is one entry of an instance's settings block. For
// non-secret settings, Value carries the actual value. For secret
// settings, Value is empty and CredentialState is "set" or "unset".
type AppSettingState struct {
	Key             string `json:"key"`
	Value           string `json:"value,omitempty"`
	CredentialState string `json:"credential_state,omitempty"`
}
type AuthConfig struct{}
type BackupConfig struct{}

// NotificationsConfig is the declarative shape of "notifications" in
// config.json. Channels carry only structural config (URL, username,
// severity filter); credentials live in the vault (DESIGN_PRINCIPLES #1).
type NotificationsConfig struct {
	Channels []NotificationChannelEntry `json:"channels,omitempty"`
}

// NotificationChannelEntry mirrors one notification_channels row for
// config.json export/import. CredentialState is "set"|"unset" (never the
// secret itself — DESIGN_PRINCIPLES #1 / forbidden list).
type NotificationChannelEntry struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"` // ntfy|webhook|smtp|line_notify|gotify
	Enabled         bool     `json:"enabled"`
	SeverityFilter  []string `json:"severity_filter,omitempty"`
	CategoryFilter  []string `json:"category_filter,omitempty"`
	CredentialState string   `json:"credential_state"` // "set"|"unset"
	// Config carries kind-specific non-secret settings (e.g. ntfy URL,
	// smtp host/port/username/from/to). Secrets are NOT here.
	Config map[string]any `json:"config,omitempty"`
}

// MonitorConfig holds metric-collection and alert-rule knobs.
type MonitorConfig struct {
	// CollectIntervalSeconds overrides the default 30-second collection cadence.
	CollectIntervalSeconds int `json:"collect_interval_seconds,omitempty"`
	// RingBufferCapacity overrides the default 23040-entry ring buffer capacity.
	RingBufferCapacity int `json:"ring_buffer_capacity,omitempty"`
	// RingBufferPath is the path to the raw.bin ring buffer file.
	// Default: /var/lib/kura/metrics/raw.bin
	RingBufferPath string `json:"ring_buffer_path,omitempty"`
	// Alerts is the set of alert rules evaluated after each metric collection.
	Alerts []AlertRuleEntry `json:"alerts,omitempty"`
}

// AlertRuleEntry mirrors engine/monitor.AlertRule for config.json.
type AlertRuleEntry struct {
	Name            string  `json:"name"`
	MetricPattern   string  `json:"metric_pattern"` // path.Match wildcard
	Op              string  `json:"op"`             // gt|lt|gte|lte|eq
	Threshold       float64 `json:"threshold"`
	Severity        string  `json:"severity"` // info|warning|critical|ok
	Category        string  `json:"category"`
	CooldownMinutes int     `json:"cooldown_minutes,omitempty"`
}

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
