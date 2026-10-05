-- API keys for programmatic access. Each key belongs to a user and carries a
-- role no higher than the owner's. Only a SHA-256 of the key is stored; the
-- prefix (e.g. ork_1a2b3c4d) identifies it for lookup and display.
CREATE TABLE api_keys (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    prefix       TEXT        NOT NULL UNIQUE,
    key_hash     BYTEA       NOT NULL,
    role_id      SMALLINT    NOT NULL REFERENCES roles (id) ON DELETE RESTRICT,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);
CREATE INDEX api_keys_user_idx ON api_keys (user_id);
