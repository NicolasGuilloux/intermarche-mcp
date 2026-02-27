# intermarche-mcp

[![build](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/build.yml/badge.svg)](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/build.yml)
[![docker](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/docker.yml/badge.svg)](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/docker.yml)

CLI (and MCP server) for the Intermarché French grocery store. Browse products,
manage your basket, and view order history by talking to the intermarche.com
API. The site is behind DataDome anti-bot; every API call is wrapped through the
Salamoonder solver, which clears it over the network — no browser anywhere (see
[DataDome](#datadome)).

> [!WARNING]
> **Disclaimer — this application was vibecoded.** It was built largely through
> AI-assisted, exploratory "vibe coding" rather than a formal engineering
> process. Expect rough edges. It is not affiliated with, endorsed by, or
> supported by Intermarché, Les Mousquetaires, or Salamoonder. Use it at your
> own risk and in accordance with the relevant terms of service.

## CLI commands

| Command | Description |
| --- | --- |
| `login` | Log in via the browser OAuth flow (run once). |
| `logout` | Log out and clear stored tokens. |
| `store set <ref>` | Select your store by PDV reference (e.g. `02334`). |
| `store search <query>` | Search stores by city name or postal code. |
| `search <query>` | Search the product catalog (store must be selected). |
| `orders` | List your previous orders. |
| `orders <number>` | Show one order with all its articles. |
| `basket get` | Show the current basket. |
| `basket add <ean> [qty]` | Add a product by EAN barcode (default qty 1). |
| `basket remove <ean> [qty]` | Remove a product by EAN barcode (default: all). |
| `mcp` | Run the MCP server over stdio. |
| `mcp http [addr]` | Run the MCP server over Streamable HTTP (default `:8080`). |

`login`, `store`, and `search` are enough to browse. `orders` and `basket`
require being logged in.

## MCP tools

When run as an MCP server (`mcp` / `mcp http`), the following tools are exposed:

| Tool | Arguments | Description |
| --- | --- | --- |
| `store_search` | `query` (required) | Search Intermarché stores by city name or postal code. |
| `store_set` | `pdv_ref` (required) | Set the active store by PDV reference number. |
| `store_get` | — | Get the currently selected store. |
| `search_products` | `query` (required), `page`, `size` | Search the product catalog. A store must be selected first. |
| `basket_get` | — | Get current cart contents. Requires login + selected store. |
| `basket_add` | `ean` (required), `quantity` | Add a product to the cart by EAN-13 barcode. Requires login + selected store. |
| `basket_remove` | `ean` (required), `quantity` | Remove a product from the cart (0 = remove all). Requires login + selected store. |
| `orders_list` | — | List recent orders. Requires login + selected store. |
| `order_detail` | `order_number` (required) | Get a specific order with all its articles. Requires login + selected store. |

Login itself is not an MCP tool — authenticate once with the `login` CLI command
(or by reusing a host session), then point your MCP client at the server.

## Run it

Pick one of the three ways below. All of them need a
[`SALAMOONDER_API_KEY`](#configuration) when using the solver transport.

> [!IMPORTANT]
> **You must log in first.** `orders`, `basket`, and the MCP server require a
> valid session. Run `login` once — it opens the browser OAuth flow and stores
> the tokens in the config dir. The MCP server has no login tool, so this CLI
> step (or reusing a host session via `IMT_CONFIG_DIR`) is mandatory before the
> server can answer authenticated requests.

### A. From sources (Go)

Tooling is provided by **Nix devenv** (Go, SQLite, gcc):

```bash
direnv allow                       # or: devenv shell
cd src && go build -o ../intermarche-mcp .
cd src && go test ./...            # optional

source .env                        # exports SALAMOONDER_API_KEY etc.
./intermarche-mcp login            # browser OAuth flow, once
./intermarche-mcp store set 02334
./intermarche-mcp search "lait demi écrémé"
./intermarche-mcp mcp http :8080   # MCP server on :8080
```

All API calls go through the Salamoonder solver, so `SALAMOONDER_API_KEY` is
required. `login` uses the browser OAuth flow once and stores the tokens in the
config dir; everything else runs headless.

### B. Docker (plain CLI)

Pull the prebuilt image from GitHub Container Registry, then run one-shot
commands or the MCP server. The `-v` bind-mount reuses a host session you've
already logged into (so auth works and no solve credit is spent — see
[DataDome & transports](#datadome--transports)).

```bash
docker pull ghcr.io/nicolasguilloux/intermarche-mcp:latest
CFG="$HOME/.config/intermarche-mcp"

# log in once (browser OAuth flow) — tokens are written to the mounted config dir
docker run --rm -it \
  -e SALAMOONDER_API_KEY="$SALAMOONDER_API_KEY" \
  -v "$CFG":/data/intermarche-mcp \
  ghcr.io/nicolasguilloux/intermarche-mcp:latest login

# one-shot command
docker run --rm \
  -e SALAMOONDER_API_KEY="$SALAMOONDER_API_KEY" \
  -v "$CFG":/data/intermarche-mcp \
  ghcr.io/nicolasguilloux/intermarche-mcp:latest search "lait demi écrémé"

# MCP server (no args → `mcp http :8080`)
docker run --rm -p 8080:8080 \
  -e SALAMOONDER_API_KEY="$SALAMOONDER_API_KEY" \
  -v "$CFG":/data/intermarche-mcp \
  ghcr.io/nicolasguilloux/intermarche-mcp:latest
```

The image is a static binary on Alpine (~24 MB) — no browser, no system deps.
Prefer building locally? `docker build -t intermarche-mcp:latest -f docker/Dockerfile .`

### C. Docker Compose

```bash
cp .env.example .env        # fill SALAMOONDER_API_KEY (+ IMT_CONFIG_DIR)
docker compose run --rm intermarche login               # browser OAuth flow, once
docker compose up --build                              # MCP server on :8080
docker compose run --rm intermarche search "lait demi écrémé"
docker compose run --rm intermarche orders
```

Set `IMT_CONFIG_DIR` in `.env` to reuse a host session; leave it empty for a
self-contained named volume (then set `SALAMOONDER_PROXY`). Full details in
[`docker/README.md`](docker/README.md).

## Configuration

Configured entirely through environment variables (see [`.env.example`](.env.example)):

| Variable | Used by | Purpose |
| --- | --- | --- |
| `SALAMOONDER_API_KEY` | solver | DataDome solver credit pool. Required. |
| `IMT_CONFIG_DIR` | compose | Host config dir to reuse inside the container (tokens + DataDome cookie). |
| `SALAMOONDER_PROXY` | solver | Residential proxy for challenge fetch + API calls (self-contained container). |
| `SALAMOONDER_MAX_SOLVES` | solver | Cap on paid solves per run (default 1). |
| `IMT_USER_AGENT` | solver | Override the Chrome User-Agent sent with requests. |

State (OAuth tokens, selected store, cached DataDome cookie) lives in the config
dir — `~/.config/intermarche-mcp` on Linux, `~/Library/Application Support/intermarche-mcp`
on macOS, or `/data/intermarche-mcp` in the container.

## DataDome

intermarche.com is protected by DataDome. Every API call is wrapped through the
**Salamoonder solver**, which clears DataDome over the network (paid, capped by
`SALAMOONDER_MAX_SOLVES`). The cleared cookie is cached on disk, so steady state
costs zero credits.

DataDome binds clearance to the IP context of the challenge: a cookie minted
*inside* a container can be rejected when the egress differs. Reuse a host
session (`IMT_CONFIG_DIR`) or set a residential `SALAMOONDER_PROXY` — details in
[`docker/README.md`](docker/README.md).

Authentication uses the browser OAuth flow (`intermarche-mcp login`); the
`desktop` Keycloak client forbids password (ROPC) login.

## Docs

- [`docker/README.md`](docker/README.md) — Docker specifics, DataDome IP-binding, session reuse
- [`docs/api-reference.md`](docs/api-reference.md) — API surface
- [`docs/add-to-basket-flow.md`](docs/add-to-basket-flow.md) — cart flow
- [`docs/authentication.md`](docs/authentication.md) — auth flow
