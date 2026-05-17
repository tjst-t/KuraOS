package user

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned by lookup methods when no row matches. We check it
// with errors.Is in callers so wrapping survives.
var ErrNotFound = errors.New("user: not found")

// ErrUsernameTaken is returned by Create when the chosen username already
// exists. Distinguished from a generic error so the UI can map it to a
// translated message instead of a 500.
var ErrUsernameTaken = errors.New("user: username already taken")

// Store wraps the SQLite store with user / group / auth_method CRUD. Every
// method takes a ctx so request cancellation propagates to the DB driver.
type Store struct {
	db     *sql.DB
	hasher Hasher
	now    func() time.Time
}

// NewStore returns a Store bound to db. hasher may be nil — in which case the
// default argon2id hasher is used. Tests pass a fast fake.
func NewStore(db *sql.DB, hasher Hasher) *Store {
	if hasher == nil {
		hasher = NewHasher()
	}
	return &Store{db: db, hasher: hasher, now: time.Now}
}

// CreateLocalUser creates a User row + a 'password' AuthMethod in a single
// transaction. Username is normalized to lowercase to make uniqueness checks
// behave predictably across input. Returns ErrUsernameTaken on UNIQUE
// constraint violation.
func (s *Store) CreateLocalUser(ctx context.Context, username, displayName, password string, role Role) (User, error) {
	if !role.Valid() {
		return User{}, fmt.Errorf("user: invalid role %q", role)
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return User{}, errors.New("user: username is empty")
	}
	// Pending users have no password until promoted — they cannot
	// authenticate via password, so we skip the hash but still insert an
	// empty auth_method row to satisfy the FK.
	isPending := role == RolePending
	if !isPending && password == "" {
		return User{}, errors.New("user: password is empty")
	}
	var hash string
	if !isPending {
		var err error
		hash, err = s.hasher.Hash(password)
		if err != nil {
			return User{}, fmt.Errorf("user: hash password: %w", err)
		}
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	uid := newID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("user: begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, disabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?)
	`, uid, username, displayName, string(role), now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrUsernameTaken
		}
		return User{}, fmt.Errorf("user: insert user: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO auth_methods (id, user_id, method, secret, subject, created_at, updated_at)
		VALUES (?, ?, 'password', ?, '', ?, ?)
	`, newID(), uid, hash, now, now)
	if err != nil {
		return User{}, fmt.Errorf("user: insert auth_method: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("user: commit: %w", err)
	}

	return s.GetByID(ctx, uid)
}

func (s *Store) GetByID(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, display_name, role, disabled, created_at, updated_at
		FROM users WHERE id = ?
	`, id)
	return scanUser(row)
}

func (s *Store) GetByUsername(ctx context.Context, username string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, display_name, role, disabled, created_at, updated_at
		FROM users WHERE username = ?
	`, username)
	return scanUser(row)
}

// List returns every persisted user, ordered by username for stable
// rendering. Used by engine/system.Reconcile to project the canonical
// user set into /etc/passwd.
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, display_name, role, disabled, created_at, updated_at
		FROM users ORDER BY username
	`)
	if err != nil {
		return nil, fmt.Errorf("user: list: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var role string
		var disabled int
		var created, updated string
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &role, &disabled, &created, &updated); err != nil {
			return nil, fmt.Errorf("user: scan: %w", err)
		}
		u.Role = Role(role)
		u.Disabled = disabled != 0
		if t, perr := time.Parse(time.RFC3339Nano, created); perr == nil {
			u.CreatedAt = t
		}
		if t, perr := time.Parse(time.RFC3339Nano, updated); perr == nil {
			u.UpdatedAt = t
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPassword overwrites the existing password row for the user. Used by
// `kura user set-password` and by future password-change UI. The argon2id
// verifier is updated in auth_methods; the corresponding NT-hash mirror
// is the responsibility of engine/system (via OnPasswordChange or a
// direct SetUserPassword call by the CLI).
func (s *Store) SetPassword(ctx context.Context, userID, password string) error {
	if password == "" {
		return errors.New("user: password is empty")
	}
	encoded, err := s.hasher.Hash(password)
	if err != nil {
		return fmt.Errorf("user: hash password: %w", err)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		UPDATE auth_methods
		SET secret = ?, updated_at = ?
		WHERE user_id = ? AND method = 'password'
	`, encoded, now, userID)
	if err != nil {
		return fmt.Errorf("user: update password: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUser overwrites the editable fields (display_name, role) of an
// existing user. Username is intentionally immutable — it is the stable
// principal name baked into Linux uid_alloc, Samba tdbsam, and ACL rows;
// renaming would silently break those projections.
func (s *Store) UpdateUser(ctx context.Context, userID, displayName string, role Role) (User, error) {
	if !role.Valid() {
		return User{}, fmt.Errorf("user: invalid role %q", role)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET display_name = ?, role = ?, updated_at = ?
		WHERE id = ?
	`, displayName, string(role), now, userID)
	if err != nil {
		return User{}, fmt.Errorf("user: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return User{}, ErrNotFound
	}
	return s.GetByID(ctx, userID)
}

// DeleteUser removes the user row. ON DELETE CASCADE handles auth_methods,
// user_groups, sessions. Vault credentials (argon2id, NT-hash) for this
// user are NOT removed here — engine/system owns the vault and its
// DeleteUser wraps both the row removal and the vault cleanup in one
// projection step. Returns ErrNotFound when no row matched.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("user: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountByRole reports how many users currently hold role. The setup wizard
// uses CountByRole(ctx, RoleAdmin) to decide whether the bootstrap form
// should be reachable.
func (s *Store) CountByRole(ctx context.Context, role Role) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ?`, string(role)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("user: count by role: %w", err)
	}
	return n, nil
}

// VerifyPassword looks up the user's password method and returns the user iff
// the supplied password matches. A non-existent username and a wrong password
// both return (User{}, false, nil) — never leak which one failed (security
// best practice; callers translate to a generic "invalid credentials" error).
func (s *Store) VerifyPassword(ctx context.Context, username, password string) (User, bool, error) {
	u, err := s.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Run the hasher anyway so timing for missing users matches valid
			// users. argon2id verify is the dominant cost; skipping it would
			// give a username-enumeration oracle.
			_, _ = s.hasher.Verify(password, "")
			return User{}, false, nil
		}
		return User{}, false, err
	}
	if u.Disabled {
		return User{}, false, nil
	}
	var encoded string
	err = s.db.QueryRowContext(ctx, `
		SELECT secret FROM auth_methods WHERE user_id = ? AND method = 'password' LIMIT 1
	`, u.ID).Scan(&encoded)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, false, nil
		}
		return User{}, false, fmt.Errorf("user: load password method: %w", err)
	}
	ok, err := s.hasher.Verify(password, encoded)
	if err != nil {
		return User{}, false, fmt.Errorf("user: verify: %w", err)
	}
	if !ok {
		return User{}, false, nil
	}
	return u, true, nil
}

func scanUser(row *sql.Row) (User, error) {
	var u User
	var role string
	var disabled int
	var created, updated string
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &role, &disabled, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("user: scan: %w", err)
	}
	u.Role = Role(role)
	u.Disabled = disabled != 0
	if t, perr := time.Parse(time.RFC3339Nano, created); perr == nil {
		u.CreatedAt = t
	}
	if t, perr := time.Parse(time.RFC3339Nano, updated); perr == nil {
		u.UpdatedAt = t
	}
	return u, nil
}

// newID returns a 32-hex-char random opaque identifier. Used for user / group
// / auth_method primary keys. The store doesn't use auto-increment so config
// import / export can preserve identity stably without exposing rowids.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is a system-level fault; panic is the only
		// honest reaction (we cannot continue safely).
		panic("user: rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// modernc.org/sqlite returns errors whose Error() string starts with
	// "constraint failed: UNIQUE". We avoid type-asserting to keep the store
	// driver-agnostic at the source level.
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "constraint failed")
}
