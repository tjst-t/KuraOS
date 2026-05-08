-- Auth foundation tables (S1e7eeb).
--
-- Local users / groups / per-user auth methods + browser sessions. Passwords
-- are never stored on the user row directly — they live on auth_methods so
-- the same user can grow additional methods (OIDC subject, future passkey)
-- without bloating the user row. argon2id verifier strings carry their own
-- params + salt so we can rotate hash parameters per row.

CREATE TABLE IF NOT EXISTS users (
    id           TEXT PRIMARY KEY,
    username     TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    role         TEXT NOT NULL CHECK (role IN ('admin','user')),
    disabled     INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS groups (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS user_groups (
    user_id  TEXT NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

-- auth_methods is the join between a user and a credential. method='password'
-- carries the argon2id verifier; future methods (oidc, passkey) carry their
-- own subject identifiers and use a NULL secret column.
CREATE TABLE IF NOT EXISTS auth_methods (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method     TEXT NOT NULL CHECK (method IN ('password','oidc','passkey')),
    -- For 'password': PHC-style argon2id verifier ($argon2id$v=19$m=...$<salt>$<hash>).
    -- For 'oidc': empty (subject lives in subject column).
    secret     TEXT NOT NULL DEFAULT '',
    -- Provider-specific identifier. For password: empty. For oidc: 'iss|sub'.
    subject    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (user_id, method, subject)
);

CREATE INDEX IF NOT EXISTS auth_methods_by_user ON auth_methods(user_id);

-- Browser sessions. id is the random opaque token presented in the cookie.
-- expires_at is enforced at lookup time. revoked_at lets logout invalidate
-- without deleting the row immediately (keeps audit trail).
CREATE TABLE IF NOT EXISTS sessions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at   TEXT NOT NULL,
    revoked_at   TEXT,
    user_agent   TEXT NOT NULL DEFAULT '',
    remote_addr  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS sessions_by_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_active  ON sessions(expires_at) WHERE revoked_at IS NULL;
