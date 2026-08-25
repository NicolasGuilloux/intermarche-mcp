# Docker (2Captcha solver)

A single static Go binary (~24 MB image). DataDome is cleared over the network
by the 2Captcha solver — no browser at runtime.

```bash
cp .env.example .env          # fill in TWOCAPTCHA_API_KEY (+ IMT_CONFIG_DIR/proxy/store)
docker compose up --build     # MCP Streamable HTTP on :8080
docker compose run --rm intermarche search "lait demi écrémé"
docker compose run --rm intermarche store search "Lille"
```

## Recommended: reuse a configured host's session

Set `IMT_CONFIG_DIR` in `.env` to a host config dir that has already been set up
(`intermarche-mcp login` + a working session):

```
# .env
IMT_CONFIG_DIR=$HOME/Library/Application Support/intermarche-mcp   # macOS
# IMT_CONFIG_DIR=$HOME/.config/intermarche-mcp                     # Linux
```

The container then shares that host's `tokens.json` (auth) and `datadome.json`
(a valid, host-minted DataDome cookie). **All commands work, no credit is
spent**, and when the cookie expires you refresh it on the host. This is the
tested path — verified with `orders`, `basket`, `search`, `store` under OrbStack
on macOS.

## Two constraints behind that recommendation

### 1. A container-minted DataDome cookie can be rejected

DataDome ties clearance to the IP context of the challenge fetch. A cookie
**minted from inside the container** can be `403`-rejected even though the same
container happily uses a cookie **minted on the host** — the solver's
challenge-fetch path inside the container differs enough to break the binding
(observed with OrbStack on macOS).

So, for a self-contained container that mints its own cookie (no
`IMT_CONFIG_DIR`), set `CAPTCHA_PROXY` to a residential proxy
(`http://user:pass@host:port`): it serves the challenge fetch *and* every API
call, stabilising the IP context regardless of where the container runs.
Reusing a host session (above) avoids this entirely.

### 2. Login requires the browser flow

The `desktop` Keycloak client forbids the password (ROPC) grant, so
`login-password` returns `Client not allowed for direct access grants`. Run the
browser flow once on a host (`intermarche-mcp login`) and share that config dir
via `IMT_CONFIG_DIR`. Unauthenticated commands (`store`, `search`) need no
token; only `orders` and `basket` do.

## Configuration

All via `.env` (see `../.env.example`): `TWOCAPTCHA_API_KEY`,
`CAPTCHA_MAX_SOLVES`, `CAPTCHA_PROXY` (or `CAPTCHA_PROXY_ADVERTISE=ngrok` plus
`NGROK_AUTHTOKEN`, which needs no ngrok binary and nothing published — the
tunnel is opened from inside the container for the length of a solve),
`IMT_CONFIG_DIR` and `IMT_USER_AGENT`.
