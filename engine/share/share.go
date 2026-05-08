// Package share owns SMB / NFS export definitions and the regeneration of
// /etc/samba/conf.d/kura.conf and /etc/exports.d/kura.exports from SQLite.
//
// The contract with the rest of KuraOS is:
//
//   - SQLite + config.json is the SSOT (DESIGN_PRINCIPLES priority #1). The
//     conf files are recomputed on every Apply — manual edits do not survive
//     a kura restart or a config.json apply.
//   - Performance / compat tunables (server multi channel support, use sendfile,
//     vfs objects, fruit:metadata, ...) are baked into the smb.conf template,
//     not surfaced in the UI (priority #2: 賢いデフォルト > 設定項目を増やす).
//   - The UI shows only: share name, path, preset (general/media/time_machine/
//     database), access mode (read_only / read_write), and a per-principal
//     ACL list. Everything else is internal.
//
// CmdExecutor wraps systemctl / testparm / exportfs so tests can run without a
// real Samba install (CLAUDE.md: dev box has no SMB/NFS).
package share

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Protocol enumerates the v1 share types. "both" emits a Samba [name] section
// and an /etc/exports.d entry from the same row so users don't have to declare
// the share twice when they want a path reachable from macOS Finder + a Linux
// NFS client.
type Protocol string

const (
	ProtocolSMB  Protocol = "smb"
	ProtocolNFS  Protocol = "nfs"
	ProtocolBoth Protocol = "both"
)

func (p Protocol) Valid() bool {
	switch p {
	case ProtocolSMB, ProtocolNFS, ProtocolBoth:
		return true
	}
	return false
}

// HasSMB / HasNFS report whether a share emits a section into the matching
// generated config file. Centralised so callers don't repeat the switch.
func (p Protocol) HasSMB() bool { return p == ProtocolSMB || p == ProtocolBoth }
func (p Protocol) HasNFS() bool { return p == ProtocolNFS || p == ProtocolBoth }

// Preset is the use-case bundle. Each preset locks a set of SMB knobs (vfs
// objects, oplocks, fruit:*, sync, strict locking) — see presets.go for the
// concrete values. The operator only ever picks the preset name.
type Preset string

const (
	PresetGeneral     Preset = "general"
	PresetMedia       Preset = "media"
	PresetTimeMachine Preset = "time_machine"
	PresetDatabase    Preset = "database"
)

func (p Preset) Valid() bool {
	switch p {
	case PresetGeneral, PresetMedia, PresetTimeMachine, PresetDatabase:
		return true
	}
	return false
}

// AccessMode is the coarse default applied when a principal in the ACL omits
// an explicit mode. Per-principal mode in ACLEntry overrides this.
type AccessMode string

const (
	AccessReadOnly  AccessMode = "read_only"
	AccessReadWrite AccessMode = "read_write"
)

func (m AccessMode) Valid() bool {
	return m == AccessReadOnly || m == AccessReadWrite
}

// PrincipalKind distinguishes user grants from group grants. Future OIDC
// federation can add 'oidc_group' as a third kind without a schema change —
// the column is a free TEXT with a CHECK constraint at the SQLite layer.
type PrincipalKind string

const (
	PrincipalUser  PrincipalKind = "user"
	PrincipalGroup PrincipalKind = "group"
)

// ACLMode is the per-principal access flag.
type ACLMode string

const (
	ACLModeReadWrite ACLMode = "rw"
	ACLModeRead      ACLMode = "r"
	ACLModeNone      ACLMode = "none"
)

func (m ACLMode) Valid() bool {
	return m == ACLModeReadWrite || m == ACLModeRead || m == ACLModeNone
}

// ACLEntry is one principal grant.
type ACLEntry struct {
	Kind PrincipalKind `json:"kind"`
	Name string        `json:"name"`
	Mode ACLMode       `json:"mode"`
}

// Share is the persisted shape. ID is opaque (random hex, matches user.newID
// style) so config.json import / export can preserve identity stably.
type Share struct {
	ID          string
	Name        string
	Path        string
	Protocol    Protocol
	Preset      Preset
	AccessMode  AccessMode
	Description string
	Disabled    bool
	ACL         []ACLEntry
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Status is a coarse runtime label for the share list. v1 only distinguishes
// active / disabled / error — the prototype's "● 3 clients" live counter is
// scope-out (no monitoring engine yet).
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusError    Status = "error"
)

// Errors that callers may want to detect specifically. Wrapped with %w in
// engine internals; top-level handlers use errors.Is to translate to i18n.
var (
	ErrShareNotFound     = errors.New("share: not found")
	ErrNameTaken         = errors.New("share: name already taken")
	ErrInvalidName       = errors.New("share: invalid name")
	ErrInvalidPath       = errors.New("share: invalid path")
	ErrPathConflict      = errors.New("share: path conflicts with existing share")
	ErrUnknownPrincipal  = errors.New("share: principal not found in user/group store")
	ErrInvalidProtocol   = errors.New("share: invalid protocol")
	ErrInvalidPreset     = errors.New("share: invalid preset")
	ErrInvalidAccessMode = errors.New("share: invalid access mode")
	ErrInvalidACL        = errors.New("share: invalid acl entry")
	ErrTestparmFailed    = errors.New("share: smb.conf failed validation")
	ErrReloadFailed      = errors.New("share: smbd / nfsd reload failed")
	ErrPathOutsideVolume = errors.New("share: path is outside any known ZFS volume")
)

// Engine is the surface the UI and config.apply pipeline consume.
type Engine interface {
	List(ctx context.Context) ([]Share, error)
	Get(ctx context.Context, id string) (Share, error)
	Create(ctx context.Context, in CreateInput) (Share, error)
	Update(ctx context.Context, id string, in UpdateInput) (Share, error)
	Delete(ctx context.Context, id string) error
	Apply(ctx context.Context) error
}

// UpdateInput is the request shape for Engine.Update. Name and Path are
// intentionally NOT editable — they're the share's stable identity from a
// client's perspective; renaming would silently break SMB / NFS mounts. To
// rename / move a share, delete + recreate. Everything else (protocol,
// preset, access mode, ACL, description, disabled) IS mutable.
type UpdateInput struct {
	Protocol    Protocol
	Preset      Preset
	AccessMode  AccessMode
	Description string
	Disabled    bool
	ACL         []ACLEntry
}

// Validate runs the same field-level checks as CreateInput minus the
// immutable ones (Name / Path).
func (in UpdateInput) Validate() error {
	if !in.Protocol.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidProtocol, in.Protocol)
	}
	if !in.Preset.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidPreset, in.Preset)
	}
	if !in.AccessMode.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidAccessMode, in.AccessMode)
	}
	for i, a := range in.ACL {
		if a.Kind != PrincipalUser && a.Kind != PrincipalGroup {
			return fmt.Errorf("%w: entry %d kind %q", ErrInvalidACL, i, a.Kind)
		}
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%w: entry %d empty name", ErrInvalidACL, i)
		}
		if !a.Mode.Valid() {
			return fmt.Errorf("%w: entry %d mode %q", ErrInvalidACL, i, a.Mode)
		}
	}
	return nil
}

// CreateInput is the request shape for Engine.Create. Validated before any
// write — the engine surfaces specific sentinel errors so the UI can map to
// translated messages.
type CreateInput struct {
	Name        string
	Path        string
	Protocol    Protocol
	Preset      Preset
	AccessMode  AccessMode
	Description string
	ACL         []ACLEntry
}

// Validate enforces field-level invariants common to every backend. Path /
// principal existence is validated by the engine after this passes — those
// require external lookups (storage / user) that this struct can't reach.
func (in CreateInput) Validate() error {
	if !validShareName(in.Name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, in.Name)
	}
	if !validSharePath(in.Path) {
		return fmt.Errorf("%w: %q", ErrInvalidPath, in.Path)
	}
	if !in.Protocol.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidProtocol, in.Protocol)
	}
	if !in.Preset.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidPreset, in.Preset)
	}
	if !in.AccessMode.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidAccessMode, in.AccessMode)
	}
	for i, a := range in.ACL {
		if a.Kind != PrincipalUser && a.Kind != PrincipalGroup {
			return fmt.Errorf("%w: entry %d kind %q", ErrInvalidACL, i, a.Kind)
		}
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%w: entry %d empty name", ErrInvalidACL, i)
		}
		if !a.Mode.Valid() {
			return fmt.Errorf("%w: entry %d mode %q", ErrInvalidACL, i, a.Mode)
		}
	}
	return nil
}

// validShareName mirrors smbd's accepted share-name characters: ASCII letters,
// digits, underscore, hyphen. We additionally cap length at 80 so a runaway
// name doesn't push the smb.conf section header onto multiple lines.
func validShareName(s string) bool {
	if s == "" || len(s) > 80 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// validSharePath requires an absolute /<volume>/<sub>... path. Any embedded
// '..' or trailing slash is rejected so the rendered conf is unambiguous.
func validSharePath(s string) bool {
	if !strings.HasPrefix(s, "/") || s == "/" {
		return false
	}
	if strings.Contains(s, "//") || strings.Contains(s, "/../") || strings.HasSuffix(s, "/..") {
		return false
	}
	if strings.HasSuffix(s, "/") {
		return false
	}
	for _, r := range s {
		// Disallow whitespace and quote characters — they break smb.conf
		// section headers and exports lines.
		if r < 0x21 || r == '"' || r == '\'' {
			return false
		}
	}
	return true
}
