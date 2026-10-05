# openresty-manager

Manage OpenResty configuration through an HTTP API and web UI. Configs are stored in
PostgreSQL as templates (trees of nginx directives with `{{variable}}`
placeholders) and rolled out to OpenResty instances through RabbitMQ.

```
             HTTP                          topic exchange "openresty"
 client ───────────▶ manager ──── config.<service>.<instance> ───▶ queue openresty.agent.<instance> ──▶ agent ─▶ OpenResty
                       │  ◀────── status.<service>.<instance> ────┐                                       │
                       │  ◀────── heartbeat.<service>.<instance> ─┴── queue openresty.api.events ◀────────┘
                       ├──▶ PostgreSQL   templates, instances, deployments, users, roles
                       └──▶ Redis        login sessions
```

There are two programs: **one manager** and **one agent per OpenResty
instance** (any number).

- **manager** (`main.go`, Fiber) serves the REST API and the embedded web UI,
  renders templates, records every up/down as a deployment row and publishes
  one command per target instance. It consumes agent results and heartbeats,
  and owns Postgres (migrations run when it starts) and Redis.
- **agent** (`cmd/agent`, logic in `internal/agent`) runs next to an
  OpenResty instance and needs only RabbitMQ. It consumes its own durable
  queue. For `up` it writes `<conf_dir>/<deployment_id>.conf` (first line
  `# config: <config_name>`) and removes the config's older files; for `down`
  it removes all of the config's files. Then it runs `openresty -t` and
  `openresty -s reload`, and restores the previous files if either fails. Commands sent while an agent is
  offline wait in its queue. An agent registers itself with the manager on
  its first heartbeat.

  The agent runs on a single goroutine: one loop receives commands (prefetch
  1), applies them one at a time and sends heartbeats in between, so two
  writes to the config directory or two reloads never overlap.
- **web** (`web/`) is a Lit + TypeScript + Tailwind + Web Awesome single-page
  app for managing instances, deployments, templates and users. It is built
  into `web/dist`, embedded in the manager binary, and served by Go routing:
  `/api/*` goes to the API, existing files are served as assets, and every
  other path returns `index.html` so client-side routes survive a reload.

## Images

The `Dockerfile` has two targets:

| Target | Base | Runs | Ports |
|---|---|---|---|
| `manager` | `alpine` | `openresty-manager` as a non-root user | `8080` (API / UI) |
| `agent` | `openresty/openresty:alpine-fat` | OpenResty + `openresty-agent` | `80` (traffic) |

```
┌──────── agent container (one per instance) ───────┐
│ entrypoint.sh                                     │
│  ├─ openresty (nginx master + workers)  :80       │◀── traffic
│  └─ openresty-agent ──writes──▶ /etc/nginx/conf.d │
│                     └─ openresty -t / -s reload   │
└───────────────────────────────────────────────────┘
```

`deploy/entrypoint.sh` starts OpenResty first, then the agent once nginx has
written its PID file (so commands queued while the instance was down are
applied only after OpenResty is running). If either process dies, the other
is stopped and the container exits for the restart policy to handle; a
`docker stop` shuts nginx down gracefully (`QUIT`) and exits 0. Mount a
volume on `/etc/nginx/conf.d` so deployed configs survive the container being
recreated.

## Users and roles

| Role | Can |
|---|---|
| `viewer` | read everything (instances, deployments, templates) and preview renders |
| `prosecutor` | everything: templates, instances, up/down, and user management |

Roles live in the `roles` table (name and description, served by
`GET /api/v1/roles`); what each role may do is enforced in code.

`POST /api/v1/auth/login` creates a session in Redis and returns its token,
both as a cookie for the UI and in the body for API clients, who send it as
`Authorization: Bearer <token>`. The cookie is `__Host-session`: `Secure`,
`HttpOnly`, `SameSite=Strict`, `Path=/`. Browsers treat `http://localhost` as
secure, so it works locally; anywhere else the UI must be served over HTTPS.

Sessions expire after `SESSION_TTL`. Redis keeps only a SHA-256 of each
token (`session:<hash>`) plus a per-user index (`user_sessions:<id>`), so:

- logout ends that session for real, not just in the browser;
- a password change or reset, or deleting the user, ends all of their sessions;
- the user and role are re-read from Postgres on every request, so role
  changes apply immediately.

Login is limited to 10 attempts per minute per IP. Prosecutors cannot delete
themselves or drop their own role, so one always remains.

The first prosecutor is created from `BOOTSTRAP_USERNAME` /
`BOOTSTRAP_PASSWORD` when the users table is empty.

```sh
curl -c jar -XPOST localhost:18080/api/v1/auth/login -d '{"username":"admin","password":"admin12345"}'
curl -b jar localhost:18080/api/v1/instances
```

## API keys

For scripts and CI, create an API key on the **API keys** page (or
`POST /api/v1/api-keys`) and send it as `X-API-Key: <key>` or
`Authorization: Bearer <key>`:

```sh
curl -H "X-API-Key: ork_1a2b3c4d_..." localhost:18080/api/v1/instances
```

- A key is shown once, at creation. The database keeps only its prefix
  (`ork_1a2b3c4d`, for lookup and display) and a SHA-256 hash.
- A key acts as the user who created it, with a role no higher than theirs
  (chosen at creation). Demoting the user also limits their keys; deleting
  the user deletes them.
- Keys can expire (30/90/365 days or never) and record when they were last
  used (updated at most once a minute).
- Keys cannot manage users, API keys or passwords; those need a signed-in
  session, so a leaked key cannot mint more keys or take over accounts.
- Deployments made with a key record `user (API key: name)` as requested by.
- Everyone manages their own keys; prosecutors can list and revoke anyone's.

## API

The full spec is in [`api/openapi.yaml`](api/openapi.yaml). All routes
except login/logout need a session; writes need the `prosecutor` role.

| Method | Path | |
|---|---|---|
| `POST` | `/api/v1/auth/login`, `/api/v1/auth/logout` | sign in / out |
| `GET` | `/api/v1/auth/me` | current user |
| `GET` | `/api/v1/roles` | roles and their descriptions |
| `GET/POST` | `/api/v1/api-keys` | list (`?all=true` for prosecutors) / create; session only |
| `DELETE` | `/api/v1/api-keys/{id}` | revoke; session only |
| `PUT` | `/api/v1/auth/password` | change your own password |
| `GET/POST` | `/api/v1/users` | list / create (prosecutor) |
| `GET/PATCH/DELETE` | `/api/v1/users/{id}` | get / change role or reset password / delete (prosecutor) |
| `POST/GET` | `/api/v1/templates` | create / list |
| `GET/PUT/DELETE` | `/api/v1/templates/{id}` | get with tree / replace / delete |
| `POST` | `/api/v1/templates/{id}/render` | preview (`?format=text` for plain text) |
| `POST` | `/api/v1/templates/{id}/directives` | add a directive subtree (`parent_id`, `position`) |
| `GET/PATCH/DELETE` | `/api/v1/templates/{id}/directives/{did}` | get / edit / delete subtree |
| `POST/GET` | `/api/v1/instances` | register / list (`?service=`) |
| `GET/PUT/DELETE` | `/api/v1/instances/{id}` | |
| `GET` | `/api/v1/instances/{id}/configs` | current up/down state per config |
| `POST` | `/api/v1/deployments/up` | render and roll out to `instances` or a whole `service` |
| `POST` | `/api/v1/deployments/down` | remove a config |
| `GET` | `/api/v1/deployments`, `/api/v1/deployments/{id}` | history and status |
| `DELETE` | `/api/v1/deployments/{id}` | remove a deployment; if its file is live, takes it down first (`202`), else `204` |

A directive with `children` (or `"block": true`) renders as a block; one with
`raw` renders its body verbatim, for `*_by_lua_block`:

```json
{
  "name": "hello-site",
  "variables": [
    {"name": "port", "default": "80"},
    {"name": "server_name", "required": true}
  ],
  "directives": [
    {"name": "server", "children": [
      {"name": "listen", "args": ["{{port}}"]},
      {"name": "server_name", "args": ["{{server_name}}"]},
      {"name": "location", "args": ["=", "/hello"], "children": [
        {"name": "content_by_lua_block", "raw": "ngx.say(\"hello\")"}
      ]}
    ]}
  ]
}
```

Then deploy it, filling the variables from the request body:

```sh
curl -b jar -XPOST localhost:18080/api/v1/deployments/up -d '{
  "template_id": 1, "config_name": "hello", "service": "edge",
  "variables": {"server_name": "demo.local"}}'
```

Deployment status goes `pending` → `published` (broker accepted) →
`applied` or `failed` (with the `openresty -t` output in `error`).

Variable values may not contain newlines, quotes, backslashes or `;{}`, and
arguments are re-checked after substitution, so a request cannot inject
directives.

## Database

Migrations in `internal/store/migrations` run automatically when the API
starts.

| Table | |
|---|---|
| `templates`, `template_variables` | template metadata and declared placeholders |
| `directives` | directive tree (adjacency list: `parent_id`, `position`) |
| `instances` | OpenResty instances; `last_seen_at` from agent heartbeats |
| `deployments` | every up/down request per instance, with rendered config and result |
| `instance_configs` | current state (`up`/`down`) of each config on each instance |
| `roles` | role names and descriptions (`viewer`, `prosecutor`) |
| `users` | usernames, bcrypt password hashes, `role_id` → `roles` |
| `api_keys` | key prefix, SHA-256 hash, owner, role, expiry, last use |

## Running locally

The supporting services and the app are separate compose projects, so you
can use Postgres, RabbitMQ and Redis you already run instead of new ones.

| File | Project | Runs |
|---|---|---|
| `compose.infra.yml` | `openresty-infra` | Postgres `:15432`, RabbitMQ `:5673` (UI `:25672`), Redis `:26379`; optional |
| `docker-compose.yml` | `openresty-manager` | the manager and the agents |

With the bundled services:

```sh
docker compose -f compose.infra.yml up -d
docker compose up -d --build
```

With existing services, skip the first command and create `compose.env`
(git-ignored) with the settings that differ from `deploy/manager.env` and
`deploy/agent.env`; it applies to the manager and every agent:

```sh
cat > compose.env <<'ENV'
DATABASE_URL=postgres://openresty:secret@db.internal:5432/openresty?sslmode=require
AMQP_URL=amqp://openresty:secret@mq.internal:5672/openresty
REDIS_URL=redis://:secret@cache.internal:6379/2
ENV
docker compose up -d --build
```

Inside a container `localhost` is the container itself; for services on the
Docker host use `host.docker.internal` (mapped in every container). The
manager needs an existing, empty Postgres database (it runs its own
migrations), a RabbitMQ user that can declare exchanges and queues on its
vhost, and any Redis database; agents need only RabbitMQ. The manager waits
up to `STARTUP_TIMEOUT` (60s) for Postgres and Redis, and both keep retrying
RabbitMQ, so start order does not matter.

Open http://localhost:18080 (the manager) and sign in as `admin` /
`admin12345` (from `deploy/manager.env`; override it in `compose.env` for
anything but local use). The agents `edge-1` and `edge-2` serve OpenResty
traffic on `:18081` and `:18082`. To add an agent, copy an `edge-N` block in
`docker-compose.yml` with a new `AGENT_INSTANCE`, port and volume; it shows up
in the UI on its first heartbeat.

Each agent keeps its `conf.d` in a named volume (`edge-1-conf`,
`edge-2-conf`). If you reset the database, also reset these
(`docker compose down -v`), or configs from before stay deployed.

To run from source, copy the example env file (both programs read `.env`, or
the file named by `ENV_FILE`; real environment variables take precedence):

```sh
docker compose -f compose.infra.yml up -d   # or point .env at existing services
cp .env.example .env
go run .                  # manager, http://localhost:8080
go run ./cmd/agent        # agent, next to a local OpenResty (set AGENT_* in .env)
```

To work on the UI with hot reload, run the API on `:8080` and the Vite dev
server, which proxies `/api` to it:

```sh
cd web && pnpm install && pnpm dev
```

Build the UI before building the manager so it gets embedded (without it the
manager still runs and serves only the API):

```sh
(cd web && pnpm install --frozen-lockfile && pnpm build)
go build -o openresty-manager .
go build -o openresty-agent ./cmd/agent
go test ./...
```

Tailwind is set up without its preflight reset and with theme values inlined
(see `web/src/styles.css`). Both are needed to coexist with Web Awesome:
preflight's `* { padding: 0 }` overrides the components' `:host` styles, and
both libraries use an unprefixed `--spacing` variable.

## Configuration

Manager: `HTTP_ADDR` (`:8080`), `DATABASE_URL`, `AMQP_URL`, `AMQP_EXCHANGE`
(`openresty`), `INSTANCE_ONLINE_WINDOW` (`90s`), `REDIS_URL`
(`redis://localhost:6379/0`), `SESSION_TTL` (`12h`), `STARTUP_TIMEOUT`
(`60s`), `BOOTSTRAP_USERNAME`, `BOOTSTRAP_PASSWORD`.

Agent: `AMQP_URL`, `AMQP_EXCHANGE`, `AGENT_INSTANCE` (hostname),
`AGENT_SERVICE` (required), `AGENT_HOST`, `AGENT_CONF_DIR`
(`/etc/nginx/conf.d`), `AGENT_TEST_CMD` (`openresty -t`), `AGENT_RELOAD_CMD`
(`openresty -s reload`), `AGENT_HEARTBEAT_INTERVAL` (`30s`),
`AGENT_COMMAND_TIMEOUT` (`30s`).

Instance and service names end up in routing keys and queue names, so they
must match `^[A-Za-z0-9][A-Za-z0-9_-]*$` (no dots).
