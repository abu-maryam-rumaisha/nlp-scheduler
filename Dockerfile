FROM node:24-alpine AS web
WORKDIR /web
# corepack installs the pnpm version pinned in package.json's packageManager.
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The API binary embeds the built UI from web/dist.
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openresty-manager . \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openresty-agent ./cmd/agent

# The manager: API and web UI. Run one.
FROM alpine:3.22 AS manager
RUN adduser -D -u 10001 app
COPY --from=build /out/openresty-manager /usr/local/bin/openresty-manager
USER app
ENV HTTP_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s \
  CMD wget -qO /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/openresty-manager"]

# An agent: OpenResty with openresty-agent alongside it. Run one per
# OpenResty instance. The stock image includes /etc/nginx/conf.d/*.conf inside
# the http block, which is where the agent writes configs.
FROM openresty/openresty:alpine-fat AS agent
ENV AGENT_CONF_DIR=/etc/nginx/conf.d
COPY --from=build /out/openresty-agent /usr/local/bin/openresty-agent
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
EXPOSE 80
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s \
  CMD kill -0 "$(cat /usr/local/openresty/nginx/logs/nginx.pid)" && pidof openresty-agent >/dev/null
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
