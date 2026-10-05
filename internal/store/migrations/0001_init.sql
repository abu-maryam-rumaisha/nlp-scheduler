-- Templates: a named tree of nginx directives used to generate a config file.
CREATE TABLE templates (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Placeholders ({{name}}) a template declares, with optional defaults.
CREATE TABLE template_variables (
    template_id   BIGINT  NOT NULL REFERENCES templates (id) ON DELETE CASCADE,
    name          TEXT    NOT NULL,
    default_value TEXT,
    required      BOOLEAN NOT NULL DEFAULT TRUE,
    description   TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (template_id, name)
);

-- Directive tree stored as an adjacency list. Deleting a directive removes
-- its whole subtree; deleting a template removes all of its directives.
CREATE TABLE directives (
    id          BIGSERIAL PRIMARY KEY,
    template_id BIGINT  NOT NULL REFERENCES templates (id) ON DELETE CASCADE,
    parent_id   BIGINT REFERENCES directives (id) ON DELETE CASCADE,
    position    INT     NOT NULL DEFAULT 0,
    name        TEXT    NOT NULL,
    args        TEXT[]  NOT NULL DEFAULT '{}',
    is_block    BOOLEAN NOT NULL DEFAULT FALSE,
    raw_body    TEXT,
    comment     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX directives_tree_idx ON directives (template_id, parent_id, position);

-- OpenResty instances, each running an agent that consumes its own queue.
CREATE TABLE instances (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT        NOT NULL UNIQUE,
    service      TEXT        NOT NULL,
    host         TEXT        NOT NULL DEFAULT '',
    description  TEXT        NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX instances_service_idx ON instances (service);

-- Every up/down request, one row per targeted instance.
CREATE TABLE deployments (
    id              BIGSERIAL PRIMARY KEY,
    instance_id     BIGINT      NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    template_id     BIGINT REFERENCES templates (id) ON DELETE SET NULL,
    config_name     TEXT        NOT NULL,
    action          TEXT        NOT NULL CHECK (action IN ('up', 'down')),
    variables       JSONB       NOT NULL DEFAULT '{}',
    rendered_config TEXT,
    status          TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'published', 'applied', 'failed')),
    error           TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX deployments_instance_idx ON deployments (instance_id, created_at DESC);
CREATE INDEX deployments_status_idx ON deployments (status);

-- Current state of each config on each instance, advanced only by deployments
-- the agent reported as applied.
CREATE TABLE instance_configs (
    instance_id   BIGINT      NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    config_name   TEXT        NOT NULL,
    template_id   BIGINT REFERENCES templates (id) ON DELETE SET NULL,
    state         TEXT        NOT NULL CHECK (state IN ('up', 'down')),
    deployment_id BIGINT      NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (instance_id, config_name)
);
