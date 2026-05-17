// Package user owns local user / group identity for KuraOS.
//
// A User is the principal that browser sessions and (future) API tokens
// belong to. Group membership is the unit of share / app authorization that
// later sprints will key off of. AuthMethod is the join between a user and a
// credential — split out from User so OIDC federation (S9db742) can attach a
// second method to an existing local user without a schema change.
//
// Passwords never appear on the User struct. They live on AuthMethod.Secret
// in PHC-encoded argon2id form (see password.go) and are produced / verified
// via Hasher so tests can inject a deterministic fake.
package user

import "time"

// Role is the coarse v1 authorization tier. VISION.json out_of_scope_permanent
// rules out fine-grained RBAC for v1; only "admin" and "user" exist.
type Role string

const (
	RoleAdmin   Role = "admin"
	RoleUser    Role = "user"
	RolePending Role = "pending"
)

func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser || r == RolePending }

// User is the persisted identity. Created / mutated through Store. The
// password verifier is kept off this struct on purpose — see AuthMethod.
type User struct {
	ID          string
	Username    string
	DisplayName string
	Role        Role
	Disabled    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Group is a named bag of users. Permissions live on the engines (share,
// app, ...) that consume the group, not on the group itself.
type Group struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
}

// AuthMethodKind enumerates the credential types a user may carry.
// v1 ships only "password"; "oidc" / "passkey" exist as schema placeholders
// so federation sprints can attach methods without migrating the table.
type AuthMethodKind string

const (
	AuthMethodPassword AuthMethodKind = "password"
	AuthMethodOIDC     AuthMethodKind = "oidc"
	AuthMethodPasskey  AuthMethodKind = "passkey"
)

// AuthMethod ties a credential to a user. Secret is the argon2id verifier for
// password methods; for OIDC it is empty and Subject holds "iss|sub". The
// store layer enforces the (user_id, method, subject) uniqueness invariant.
type AuthMethod struct {
	ID        string
	UserID    string
	Method    AuthMethodKind
	Secret    string
	Subject   string
	CreatedAt time.Time
	UpdatedAt time.Time
}
