# ── Build stage ───────────────────────────────────────────────────────────────
# Pinned to the toolchain in go.mod (1.25.x) for reproducible builds.
# CGO is disabled: the only C-ish dependency (modernc.org/sqlite) is pure Go,
# so the result is a fully static binary that runs on a bare Alpine.
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /intermarche-mcp .

# ── Runtime stage ─────────────────────────────────────────────────────────────
# The Salamoonder solver clears DataDome over the network (internal/solver), so
# there is no browser at runtime — all that's needed is a CA bundle for TLS.
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app

COPY --from=builder /intermarche-mcp /usr/local/bin/intermarche-mcp
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# MCP HTTP on :8080, and config (tokens, selected store, cached DataDome cookie)
# under the mounted /data volume. XDG_CONFIG_HOME drives os.UserConfigDir() →
# /data/intermarche-mcp.
ENV MCP_ADDR=:8080 \
    XDG_CONFIG_HOME=/data

RUN mkdir -p /data/intermarche-mcp && chown -R app:app /data
USER app
VOLUME ["/data"]

# MCP Streamable HTTP
EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
