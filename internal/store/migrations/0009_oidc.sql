-- S822961: OIDC Provider state.
--
-- Three tables back the in-house OIDC OP that lives in engine/auth/oidc:
--
--  - oidc_clients       : registered RPs (auto-created on app install when
--                         manifest.auth.mode == "oidc"). client_secret is NOT
--                         stored here — it lives in the vault keyed by the
--                         (kind=oidc_client_secret, owner=app, owner_id=client_id)
--                         tuple. This table only carries declarative metadata
--                         that round-trips through config.json export.
--  - oidc_auth_codes    : short-lived (60s) one-shot authorization codes.
--                         Issued at /oidc/authorize, consumed at /oidc/token.
--                         consumed_at NOT NULL prevents replay.
--  - federation_links   : maps a KuraOS user_id to an external IdP subject so
--                         repeated Google logins resolve to the same user.

CREATE TABLE IF NOT EXISTS oidc_clients (
    client_id           TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    redirect_uris       TEXT NOT NULL,            -- JSON array of strings
    grant_types         TEXT NOT NULL DEFAULT '["authorization_code"]',
    response_types      TEXT NOT NULL DEFAULT '["code"]',
    scopes              TEXT NOT NULL DEFAULT '["openid","profile","email"]',
    token_endpoint_auth TEXT NOT NULL DEFAULT 'client_secret_basic',
    app_id              TEXT,                      -- nullable: NULL for non-app clients
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS oidc_auth_codes (
    code                TEXT PRIMARY KEY,
    client_id           TEXT NOT NULL,
    user_id             TEXT NOT NULL,
    redirect_uri        TEXT NOT NULL,
    scope               TEXT NOT NULL,
    nonce               TEXT NOT NULL DEFAULT '',
    code_challenge      TEXT NOT NULL DEFAULT '',
    code_challenge_method TEXT NOT NULL DEFAULT '',
    issued_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at          TEXT NOT NULL,
    consumed_at         TEXT
);

CREATE INDEX IF NOT EXISTS oidc_auth_codes_expires_idx ON oidc_auth_codes(expires_at);

CREATE TABLE IF NOT EXISTS federation_links (
    provider            TEXT NOT NULL,
    subject             TEXT NOT NULL,
    user_id             TEXT NOT NULL,
    email               TEXT NOT NULL DEFAULT '',
    linked_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX IF NOT EXISTS federation_links_user_idx ON federation_links(user_id);
