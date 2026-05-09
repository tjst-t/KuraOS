// Package system owns the projection of KuraOS users / groups into Linux
// uid / gid, the unified credential vault, and the system-file fragments
// (/etc/passwd, /etc/group, /etc/samba/smb.conf include) that downstream
// daemons (smbd, nfsd, sshd, docker) depend on.
//
// DESIGN_PRINCIPLES priority #10 makes this the SOLE owner of those edits.
// Other engines (share, app, backup, ...) MUST go through System rather than
// shelling out to useradd / smbpasswd / chown / chmod or editing /etc/* files
// directly.
//
// Cross-protocol identity flow:
//
//	engine/user.Create  -- triggers -->  System.SyncUsers / SyncGroups
//	  -> uid_alloc table picks a stable uid from 30000-39999
//	  -> /etc/passwd "# BEGIN KuraOS managed" block is rewritten
//	  -> /etc/group "# BEGIN KuraOS managed" block is rewritten
//	  -> NSS sees the new user immediately (file backend, no nscd flush needed)
//
//	engine/user.SetPassword  -- triggers -->  System.SetUserPassword
//	  -> argon2id verifier written to credentials vault (kind='argon2id')
//	  -> NT-hash (MD4 of UTF-16LE) written to credentials vault (kind='nt_hash')
//	  -> pdbedit imports the smbpasswd-format line into Samba tdbsam
//
//	engine/share.Create / Update  -- triggers -->  System.ApplyShareOwnership
//	  -> chown <uid>:<gid> + chmod 2775 on the share path
//	  -> share_perm_state caches the applied tuple for idempotent re-runs
//
// The runtime model is "credential-at-rest is plaintext under root:0600
// state.db; encryption is a backup-time concern". Backup tarballs apply
// age passphrase encryption to the vault before writing to disk.
package system

import (
	"context"
	"errors"
)

// UID range reserved for KuraOS-managed users. Outside the Ubuntu apt
// service uid range (<1000) and outside the systemd DynamicUser range
// (61184-65519), so distro-installed users and KuraOS users never collide.
const (
	UIDMin = 30000
	UIDMax = 39999
	GIDMin = 30000
	GIDMax = 39999
)

// Marker lines that bracket the KuraOS-managed block in /etc/passwd and
// /etc/group. The reconcile rewrites only the lines between these markers;
// distro / sysadmin entries outside are preserved verbatim.
const (
	BeginMarker = "# BEGIN KuraOS managed -- do not edit by hand"
	EndMarker   = "# END KuraOS managed"
)

// PrimaryGroupName is the default primary Linux group every KuraOS user is
// placed in. KuraOS-shared datasets are owned by this group with mode 2775
// so per-user files can still be group-readable.
const PrimaryGroupName = "kura-users"

// Errors callers may want to detect specifically.
var (
	ErrUIDExhausted    = errors.New("system: uid range 30000-39999 exhausted")
	ErrGIDExhausted    = errors.New("system: gid range 30000-39999 exhausted")
	ErrUserNotFound    = errors.New("system: user not found")
	ErrCredentialEmpty = errors.New("system: credential value is empty")
)

// ProjectedUser is the system-side view of a KuraOS user. It is what
// engine/system writes into /etc/passwd and what other engines look up to
// get a stable uid for chown / docker run --user / etc.
type ProjectedUser struct {
	UserID      string
	Username    string
	DisplayName string
	UID         int
	GID         int
	Disabled    bool
	HomeDir     string
	Shell       string
}

// ProjectedGroup is the system-side view of a KuraOS group.
type ProjectedGroup struct {
	GroupID string
	Name    string
	GID     int
	Members []string
}

// CredentialKind enumerates the recognised vault rows. New kinds are added
// as engines arrive (oidc_client_secret, app_db_password, tls_key, ...) —
// the table is open by design so the schema does not need bumping per kind.
type CredentialKind string

const (
	CredentialArgon2id              CredentialKind = "argon2id"
	CredentialNTHash                CredentialKind = "nt_hash"
	CredentialOIDCClientSecret      CredentialKind = "oidc_client_secret"
	CredentialOIDCSigningKey        CredentialKind = "oidc_signing_key"
	CredentialFederationClientSec   CredentialKind = "federation_client_secret"
	CredentialAppDBPassword         CredentialKind = "app_db_password"
	CredentialTLSKey                CredentialKind = "tls_key"
)

// CredentialOwnerKind partitions the vault rows by the kind of entity that
// owns them, so the (kind, owner_kind, owner_id) tuple stays unique.
type CredentialOwnerKind string

const (
	OwnerUser   CredentialOwnerKind = "user"
	OwnerGroup  CredentialOwnerKind = "group"
	OwnerApp    CredentialOwnerKind = "app"
	OwnerSystem CredentialOwnerKind = "system"
)

// Credential is one vault row. Value semantics depend on Kind.
type Credential struct {
	ID        string
	Kind      CredentialKind
	OwnerKind CredentialOwnerKind
	OwnerID   string
	Value     string
}

// PlaintextPassword is a one-shot wrapper around a UTF-8 password string.
// Callers obtain it from the UI / CLI, hand it to System.SetUserPassword,
// and let the engine zero the underlying byte slice when projection is
// done. The struct does NOT implement Stringer or Marshal so it cannot
// accidentally escape into log lines or JSON.
//
// Use:
//
//	pt := system.NewPlaintextPassword(formValue)
//	defer pt.Zero()
//	if err := sys.SetUserPassword(ctx, userID, pt); err != nil { ... }
type PlaintextPassword struct {
	bytes []byte
}

// NewPlaintextPassword copies s into an internal byte slice that Zero
// can blank later. Returning a struct (not a pointer) keeps the caller
// responsible for liveness — the slice header lives on their stack.
func NewPlaintextPassword(s string) PlaintextPassword {
	b := make([]byte, len(s))
	copy(b, s)
	return PlaintextPassword{bytes: b}
}

// Bytes returns the raw plaintext bytes for a single use. Caller must not
// retain the returned slice after Zero.
func (p PlaintextPassword) Bytes() []byte { return p.bytes }

// String returns the plaintext as a UTF-8 string. Used by NT-hash projection
// (which encodes to UTF-16LE) and by argon2id hashing.
func (p PlaintextPassword) String() string { return string(p.bytes) }

// Zero overwrites the underlying byte slice with zero. Idempotent.
func (p PlaintextPassword) Zero() {
	for i := range p.bytes {
		p.bytes[i] = 0
	}
}

// Engine is the surface other engines and the CLI consume. Implementation
// is in service.go. Tests inject mocks at the FileSystem and CmdExecutor
// boundary rather than reimplementing this interface.
type Engine interface {
	// AllocateUID returns the (stable) uid for userID, allocating a new
	// one in the 30000-39999 range on first call. Subsequent calls with
	// the same userID always return the same uid.
	AllocateUID(ctx context.Context, userID string) (int, error)

	// AllocateGID is the group analogue of AllocateUID.
	AllocateGID(ctx context.Context, groupID string) (int, error)

	// LookupUID returns the previously-allocated uid for userID, or
	// ErrUserNotFound if AllocateUID has never been called for it.
	LookupUID(ctx context.Context, userID string) (int, error)

	// SetUserPassword writes both the argon2id verifier and the NT-hash
	// mirror into the vault in a single SQL transaction, then projects
	// the NT-hash into Samba tdbsam via pdbedit. The plaintext is zeroed
	// before this method returns.
	SetUserPassword(ctx context.Context, userID string, pw PlaintextPassword) error

	// Reconcile re-projects the SQLite users/groups/credentials state
	// onto /etc/passwd, /etc/group, /etc/samba/smb.conf include, and
	// every share path's chown/chmod. Idempotent. Called at startup
	// and via `kura system reconcile`.
	Reconcile(ctx context.Context) error

	// ApplyShareOwnership chowns/chmods sharePath based on the share's
	// ACL (group ownership comes from first 'group' principal with rw
	// mode; falls back to PrimaryGroupName).
	ApplyShareOwnership(ctx context.Context, share ShareTarget) error

	// LookupCredential returns the vault row matching kind/owner. Used
	// by backup, by the OIDC sprint to fetch client secrets, etc.
	LookupCredential(ctx context.Context, kind CredentialKind, owner CredentialOwnerKind, ownerID string) (Credential, error)

	// SetCredential upserts a vault row. Used by future engines (OIDC,
	// app installer) to register their secrets.
	SetCredential(ctx context.Context, c Credential) error

	// CreateUser is the high-level transactional projection: creates the
	// engine/user row, allocates a uid, sets the password (argon2id +
	// NT-hash + Samba tdbsam), and re-projects /etc/passwd. Failure at
	// any step rolls back the entire chain so the caller never observes
	// a half-created user. (Sfix001-1 AC-3.)
	CreateUser(ctx context.Context, in CreateUserInput) (string, error)

	// UpdateUser modifies the editable fields (display_name, role).
	// Username is immutable — see engine/user.Store.UpdateUser comment.
	UpdateUser(ctx context.Context, userID, displayName, role string) error

	// DeleteUser removes the user row, clears all vault credentials
	// owned by that user (argon2id, nt_hash), and re-projects
	// /etc/passwd. uid_alloc is intentionally retained so a same-name
	// user re-created later lands on the same uid. (Sfix001-1 AC-3.)
	DeleteUser(ctx context.Context, userID string) error

	// CreateGroup creates an engine/user group row, allocates a stable
	// gid in [GIDMin, GIDMax], and re-projects /etc/group. Returns
	// the new group ID.
	CreateGroup(ctx context.Context, name, description string) (string, error)

	// DeleteGroup removes the group. Caller (UI) MUST first call
	// ListSharesReferencingGroup to confirm no Share ACL still names
	// it; this method does NOT enforce that integrity check itself
	// because share lookup belongs in the UI/orchestration layer to
	// keep engine/system free of an engine/share import. (Sfix001-2
	// AC-3.)
	DeleteGroup(ctx context.Context, groupID string) error

	// SetGroupMembers replaces the membership list of groupID with
	// userIDs and re-projects /etc/group secondary group entries.
	SetGroupMembers(ctx context.Context, groupID string, userIDs []string) error
}

// CreateUserInput is the request shape for Engine.CreateUser.
type CreateUserInput struct {
	Username    string
	DisplayName string
	Password    string
	Role        string
}

// ShareTarget is the slice of share data engine/system needs to chown
// the path. Reduced from the full share.Share to keep the engine/system
// import boundary thin (no circular dependency on engine/share).
type ShareTarget struct {
	ID   string
	Name string
	Path string
	// Group hints — the first entry is preferred, later entries are
	// fallbacks. Empty list means use PrimaryGroupName.
	GroupNames []string
}
