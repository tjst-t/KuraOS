-- engine/system foundation tables (Ssys001).
--
-- Cross-protocol identity needs three things:
--   1. Stable uid/gid per KuraOS user/group (uid_alloc / gid_alloc).
--   2. A single credential vault that holds every secret KuraOS owns
--      (argon2id verifiers, NT-hash mirror, future OIDC client secret,
--      app DB passwords, TLS keys, etc.). DESIGN_PRINCIPLES priority #1.
--   3. A way to record which share path got chown'd to which gid so a
--      reconcile can detect drift without re-stat'ing every share.
--
-- All values live in state.db which is already root:0600 in production.

-- uid_alloc is the persistent uid <-> user_id map. Same user_id -> same uid
-- across restore (priority #1 round-trip invariant). No FK to users() so
-- a backup tarball can be restored into a state.db that hasn't yet
-- replayed the user creates from config.json — the vault is the priority
-- #1 SSOT and must round-trip independently of structural state.
CREATE TABLE IF NOT EXISTS uid_alloc (
    user_id      TEXT PRIMARY KEY,
    uid          INTEGER NOT NULL UNIQUE,
    allocated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- gid_alloc holds both real engine/user groups (FK enforced by app) and
-- synthetic system groups (e.g. '@@kura-users') keyed by an internal name
-- the engine reserves. We deliberately omit the FK so the synthetic rows
-- don't need a matching groups(id) entry.
CREATE TABLE IF NOT EXISTS gid_alloc (
    group_id   TEXT PRIMARY KEY,
    gid        INTEGER NOT NULL UNIQUE,
    allocated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- credentials is the unified vault. owner_kind/owner_id is a polymorphic
-- foreign key (no DB-level constraint — same row can refer to a user, a
-- service, an app instance, or be system-owned with owner_id=''). kind
-- enumerates the credential type so the consumer (Samba projection,
-- backup/restore, app installer) can filter without parsing value.
--
-- value is opaque text — the kind dictates the encoding:
--   'argon2id'   : PHC-encoded argon2id verifier
--   'nt_hash'    : 32-char hex MD4 of UTF-16LE plaintext
--   'oidc_client_secret' / 'app_db_password' / 'tls_key' : raw secret
--
-- The (kind, owner_kind, owner_id) tuple is unique so a user has at most
-- one row per credential kind.
CREATE TABLE IF NOT EXISTS credentials (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,
    owner_kind  TEXT NOT NULL CHECK (owner_kind IN ('user','group','app','system')),
    owner_id    TEXT NOT NULL DEFAULT '',
    value       TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (kind, owner_kind, owner_id)
);

CREATE INDEX IF NOT EXISTS credentials_by_owner ON credentials(owner_kind, owner_id);

-- share_perm_state caches the last-applied owner/group/mode per share path
-- so engine/system.ApplyShareOwnership can be a no-op on idempotent runs.
-- No FK to shares(id) — engine/share is responsible for orphan cleanup
-- (Delete + Reconcile rebuilds the table) and tests routinely synthesise
-- share IDs without round-tripping through engine/share.
CREATE TABLE IF NOT EXISTS share_perm_state (
    share_id    TEXT PRIMARY KEY,
    path        TEXT NOT NULL,
    owner_uid   INTEGER NOT NULL,
    owner_gid   INTEGER NOT NULL,
    mode        INTEGER NOT NULL,
    applied_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
