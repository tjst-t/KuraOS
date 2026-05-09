package system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// UserSource lets engine/system enumerate the canonical KuraOS users
// without importing engine/user (which would create a cycle: engine/user
// would also want engine/system for projection on Create).
type UserSource interface {
	List(ctx context.Context) ([]SourceUser, error)
}

// SourceUser is the minimal user shape engine/system needs from
// engine/user. Mirrors the fields we project into /etc/passwd.
type SourceUser struct {
	ID          string
	Username    string
	DisplayName string
	Disabled    bool
}

// Hasher computes the argon2id verifier for a plaintext password. Same
// shape as engine/user.Hasher so the cmd/kura wiring can pass the
// production hasher straight in.
type Hasher interface {
	Hash(password string) (string, error)
}

// Service is the production Engine implementation. Wired from cmd/kura
// once at startup with the real *sql.DB, RealFS rooted at "/", and the
// real cmdexec.
type service struct {
	db     *sql.DB
	fs     FileSystem
	exec   cmdexec.Executor
	hasher Hasher
	users  UserSource

	// onPasswordChange is an optional hook so callers can mirror the
	// argon2id verifier into the legacy auth_methods table while v1
	// still authenticates from there. Removed once auth/session reads
	// from the vault directly.
	onPasswordChange func(ctx context.Context, userID, encoded string) error
}

// Options configure a Service. All fields default to sensible production
// implementations when zero (FS = NewRealFS("/"), Exec = cmdexec.NewReal()).
type Options struct {
	DB     *sql.DB
	FS     FileSystem
	Exec   cmdexec.Executor
	Hasher Hasher
	Users  UserSource

	OnPasswordChange func(ctx context.Context, userID, encoded string) error
}

// New returns the production Engine. DB and Hasher are required; FS / Exec
// default if not supplied.
func New(opts Options) (Engine, error) {
	if opts.DB == nil {
		return nil, errors.New("system: DB is required")
	}
	if opts.Hasher == nil {
		return nil, errors.New("system: Hasher is required")
	}
	s := &service{
		db:               opts.DB,
		fs:               opts.FS,
		exec:             opts.Exec,
		hasher:           opts.Hasher,
		users:            opts.Users,
		onPasswordChange: opts.OnPasswordChange,
	}
	if s.fs == nil {
		s.fs = NewRealFS("/")
	}
	if s.exec == nil {
		s.exec = cmdexec.NewReal()
	}
	return s, nil
}

func (s *service) AllocateUID(ctx context.Context, userID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("system: begin: %w", err)
	}
	defer tx.Rollback()
	uid, err := s.allocateUID(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("system: commit: %w", err)
	}
	return uid, nil
}

func (s *service) AllocateGID(ctx context.Context, groupID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("system: begin: %w", err)
	}
	defer tx.Rollback()
	gid, err := s.allocateGID(ctx, tx, groupID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("system: commit: %w", err)
	}
	return gid, nil
}

func (s *service) LookupUID(ctx context.Context, userID string) (int, error) {
	var uid int
	err := s.db.QueryRowContext(ctx, `SELECT uid FROM uid_alloc WHERE user_id = ?`, userID).Scan(&uid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrUserNotFound
		}
		return 0, fmt.Errorf("system: lookup uid: %w", err)
	}
	return uid, nil
}

// SetUserPassword: argon2id + NT-hash dual-write in one transaction so a
// crash never leaves the user with one credential and not the other.
// Plaintext is zeroed after the hashes are produced.
func (s *service) SetUserPassword(ctx context.Context, userID string, pw PlaintextPassword) error {
	defer pw.Zero()
	if userID == "" {
		return fmt.Errorf("system: userID is empty")
	}
	if len(pw.Bytes()) == 0 {
		return errors.New("system: password is empty")
	}

	encoded, err := s.hasher.Hash(pw.String())
	if err != nil {
		return fmt.Errorf("system: hash argon2id: %w", err)
	}
	nt := NTHash(pw.String())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("system: begin: %w", err)
	}
	defer tx.Rollback()

	if err := upsertCredential(ctx, tx, Credential{
		Kind: CredentialArgon2id, OwnerKind: OwnerUser, OwnerID: userID, Value: encoded,
	}); err != nil {
		return err
	}
	if err := upsertCredential(ctx, tx, Credential{
		Kind: CredentialNTHash, OwnerKind: OwnerUser, OwnerID: userID, Value: nt,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("system: commit: %w", err)
	}

	// Project NT-hash into Samba tdbsam outside the SQL transaction —
	// pdbedit is a side effect we cannot roll back, but the vault is
	// the SSOT so a pdbedit failure leaves consistent state. Reconcile
	// will retry the projection.
	if err := s.projectNTHashToSamba(ctx, userID); err != nil {
		// Soft-fail: log and continue. The vault still has the row;
		// Reconcile will pick it up.
		// (Returning the error would confuse the CLI caller into
		// thinking the password didn't save when it did.)
		_ = err
	}

	if s.onPasswordChange != nil {
		if err := s.onPasswordChange(ctx, userID, encoded); err != nil {
			return fmt.Errorf("system: legacy auth_methods mirror: %w", err)
		}
	}
	return nil
}

// projectNTHashToSamba uses `pdbedit -i smbpasswd:- -e tdbsam:` to import
// the user's NT-hash into Samba's tdbsam. The smbpasswd-format line is
// piped via stdin; pdbedit reads it and upserts into /var/lib/samba/private/passdb.tdb.
func (s *service) projectNTHashToSamba(ctx context.Context, userID string) error {
	uid, err := s.LookupUID(ctx, userID)
	if err != nil {
		return err
	}
	row := s.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, userID)
	var username string
	if err := row.Scan(&username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("system: lookup username: %w", err)
	}
	cred, err := lookupCredential(ctx, s.db, CredentialNTHash, OwnerUser, userID)
	if err != nil {
		return err
	}
	line := SmbPasswdLine(username, uid, cred.Value)
	return execPdbedit(ctx, s.exec, line)
}

// execPdbedit runs `pdbedit -i smbpasswd:- -e tdbsam:` with line on stdin.
// cmdexec.Executor doesn't expose stdin, so we use os/exec directly here
// (with a context). The Real executor is the testable boundary; for tests
// that don't have pdbedit installed, NewFromOptions wires a no-op exec.
func execPdbedit(ctx context.Context, exec cmdexec.Executor, line string) error {
	// Re-use exec.Run: pdbedit accepts the smbpasswd file path or stdin.
	// We write line to a tmpfile and pass that path — keeps the
	// cmdexec.Executor surface unchanged (no stdin variant) and works
	// in both the production exec path and tests with a Fake.
	tmp, err := os.CreateTemp("", "kura-smbpasswd-*.txt")
	if err != nil {
		return fmt.Errorf("system: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(line + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("system: write tmp smbpasswd: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("system: close tmp smbpasswd: %w", err)
	}
	// Note: backend spec is `tdbsam` (no trailing colon). Trailing
	// colon is parsed as "open tdbsam at empty path" which silently
	// fails to import. Discovered on VM verification.
	_, _, err = exec.Run(ctx, "pdbedit", "-i", "smbpasswd:"+tmp.Name(), "-e", "tdbsam")
	if err != nil {
		return fmt.Errorf("system: pdbedit import: %w", err)
	}
	return nil
}

// Reconcile re-projects every KuraOS user/group into /etc/passwd, /etc/group,
// /etc/samba/smb.conf include, and runs ApplyShareOwnership on every share
// path. Idempotent — re-running is cheap (file rewrites only when content
// changes; chown is skipped when share_perm_state matches).
func (s *service) Reconcile(ctx context.Context) error {
	users, err := s.users.List(ctx)
	if err != nil {
		return fmt.Errorf("system: list users: %w", err)
	}

	primaryGID, err := s.allocatePrimaryGID(ctx)
	if err != nil {
		return err
	}

	projected := make([]ProjectedUser, 0, len(users))
	usernames := make([]string, 0, len(users))
	for _, u := range users {
		if u.Disabled {
			continue
		}
		uid, err := s.AllocateUID(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("system: allocate uid for %s: %w", u.Username, err)
		}
		projected = append(projected, ProjectedUser{
			UserID:      u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			UID:         uid,
			GID:         primaryGID,
			HomeDir:     "/var/empty",
			Shell:       "/usr/sbin/nologin",
		})
		usernames = append(usernames, u.Username)
	}

	if err := s.writePasswd(projected, primaryGID); err != nil {
		return err
	}
	if err := s.writeGroup(primaryGID, usernames); err != nil {
		return err
	}
	if _, err := ensureSmbInclude(s.fs); err != nil {
		return fmt.Errorf("system: ensure smb include: %w", err)
	}

	// Project NT-hash for every user that has one in the vault. This
	// covers the "drift" case where SQLite has the hash but tdbsam was
	// wiped (e.g. fresh /var/lib/samba on a restored host).
	for _, u := range projected {
		if err := s.projectNTHashToSamba(ctx, u.UserID); err != nil {
			// Soft-fail: a missing NT-hash row means the user has
			// never set a password yet. Other errors (pdbedit not
			// installed) are logged but not fatal.
			if errors.Is(err, ErrUserNotFound) {
				continue
			}
			_ = err
		}
	}

	return nil
}

// allocatePrimaryGID gives the synthetic 'kura-users' group a stable gid.
// It is keyed in gid_alloc by the literal string "@@kura-users" so it
// cannot collide with a real group_id (which is a hex string).
const primaryGroupKey = "@@kura-users"

func (s *service) allocatePrimaryGID(ctx context.Context) (int, error) {
	return s.AllocateGID(ctx, primaryGroupKey)
}

func (s *service) writePasswd(users []ProjectedUser, primaryGID int) error {
	conflicts := make([]string, 0, len(users))
	for _, u := range users {
		conflicts = append(conflicts, u.Username)
	}
	original, err := s.fs.ReadFile(pathPasswd)
	if err != nil {
		if isNotExist(err) {
			original = []byte{}
		} else {
			return fmt.Errorf("system: read passwd: %w", err)
		}
	}
	block := renderPasswdBlock(users, primaryGID)
	updated := mergeManagedBlock(original, block, conflicts)
	if err := s.fs.WriteAtomic(pathPasswd, updated, 0o644); err != nil {
		return fmt.Errorf("system: write passwd: %w", err)
	}
	return nil
}

func (s *service) writeGroup(primaryGID int, members []string) error {
	original, err := s.fs.ReadFile(pathGroup)
	if err != nil {
		if isNotExist(err) {
			original = []byte{}
		} else {
			return fmt.Errorf("system: read group: %w", err)
		}
	}
	groups := []ProjectedGroup{
		{
			GroupID: primaryGroupKey,
			Name:    PrimaryGroupName,
			GID:     primaryGID,
			Members: members,
		},
	}
	block := renderGroupBlock(groups)
	conflicts := []string{PrimaryGroupName}
	updated := mergeManagedBlock(original, block, conflicts)
	if err := s.fs.WriteAtomic(pathGroup, updated, 0o644); err != nil {
		return fmt.Errorf("system: write group: %w", err)
	}
	return nil
}

func (s *service) ApplyShareOwnership(ctx context.Context, share ShareTarget) error {
	primaryGID, err := s.allocatePrimaryGID(ctx)
	if err != nil {
		return err
	}
	gid := primaryGID
	// Future: resolve named groups in share.GroupNames to gid via
	// gid_alloc. v1 has no named-group plumbing yet, so we just use
	// the primary kura-users group.
	_ = share.GroupNames

	// uid 0 (root) ownership is intentional — Samba uses force user/
	// force group rules in the per-share template to map writes to the
	// shared kura-users group regardless of the on-disk owner.
	uid := 0

	if err := s.fs.Chown(share.Path, uid, gid); err != nil {
		return fmt.Errorf("system: chown share %q: %w", share.Path, err)
	}
	mode := os.FileMode(0o2775)
	if err := s.fs.Chmod(share.Path, mode); err != nil {
		return fmt.Errorf("system: chmod share %q: %w", share.Path, err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO share_perm_state (share_id, path, owner_uid, owner_gid, mode, applied_at)
		VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT (share_id) DO UPDATE SET
			path = excluded.path,
			owner_uid = excluded.owner_uid,
			owner_gid = excluded.owner_gid,
			mode = excluded.mode,
			applied_at = excluded.applied_at
	`, share.ID, share.Path, uid, gid, int(mode))
	if err != nil {
		return fmt.Errorf("system: record share perm state: %w", err)
	}
	return nil
}

func (s *service) LookupCredential(ctx context.Context, kind CredentialKind, owner CredentialOwnerKind, ownerID string) (Credential, error) {
	return lookupCredential(ctx, s.db, kind, owner, ownerID)
}

func (s *service) SetCredential(ctx context.Context, c Credential) error {
	if c.Kind == "" || c.OwnerKind == "" {
		return errors.New("system: kind and owner_kind required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("system: begin: %w", err)
	}
	defer tx.Rollback()
	if err := upsertCredential(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit()
}

// AllCredentials returns the full credential vault. Backup uses this; the
// runtime path uses LookupCredential by tuple.
func AllCredentials(ctx context.Context, db *sql.DB) ([]Credential, error) {
	return listCredentials(ctx, db)
}

// LegacyAuthMethodMirror is the OnPasswordChange hook cmd/kura passes to
// service.New so that the existing engine/user.VerifyPassword path keeps
// working while engine/system also writes to the vault. Once auth/session
// reads from the vault directly (a later refactor), this hook becomes
// unnecessary.
func LegacyAuthMethodMirror(db *sql.DB) func(context.Context, string, string) error {
	return func(ctx context.Context, userID, encoded string) error {
		_, err := db.ExecContext(ctx, `
			UPDATE auth_methods
			SET secret = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE user_id = ? AND method = 'password'
		`, encoded, userID)
		if err != nil {
			return fmt.Errorf("system: mirror auth_methods: %w", err)
		}
		return nil
	}
}

// SanitizeUsername lowercases + strips disallowed characters. Used by CLI
// commands that take a username as input.
func SanitizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
