-- Roles a user can hold. What each role may do is enforced in code
-- (internal/auth); this table names and describes them.
CREATE TABLE roles (
    id          SMALLSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO roles (name, description) VALUES
    ('viewer', 'Read-only access to instances, deployments and templates.'),
    ('prosecutor', 'Full access: templates, instances, deployments and user management.');

-- API users. Sessions live in Redis, not here.
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role_id       SMALLINT    NOT NULL REFERENCES roles (id) ON DELETE RESTRICT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX users_role_idx ON users (role_id);

-- Who asked for each up/down.
ALTER TABLE deployments ADD COLUMN requested_by TEXT NOT NULL DEFAULT '';
