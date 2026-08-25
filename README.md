# intermarche-mcp

[![build](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/build.yml/badge.svg)](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/build.yml)
[![docker](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/docker.yml/badge.svg)](https://github.com/NicolasGuilloux/intermarche-mcp/actions/workflows/docker.yml)

CLI (and MCP server) for the Intermarché French grocery store. Browse products,
manage your basket, and view order history by talking to the intermarche.com
API. The site is behind DataDome anti-bot; every API call is wrapped through a
captcha solver (2Captcha), which clears it over the network — no browser
anywhere (see [DataDome](#datadome)).

> [!WARNING]
> **Disclaimer — this application was vibecoded.** It was built largely through
> AI-assisted, exploratory "vibe coding" rather than a formal engineering
> process. Expect rough edges. It is not affiliated with, endorsed by, or
> supported by Intermarché, Les Mousquetaires, or 2Captcha. Use it at your
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

Pick one of the three ways below. All of them need a captcha solver key —
`TWOCAPTCHA_API_KEY` — and a proxy (see [DataDome](#datadome)).

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

source .env                        # exports TWOCAPTCHA_API_KEY etc.
./intermarche-mcp login            # browser OAuth flow, once
./intermarche-mcp store set 02334
./intermarche-mcp search "lait demi écrémé"
./intermarche-mcp mcp http :8080   # MCP server on :8080
```

All API calls go through a captcha solver, so `TWOCAPTCHA_API_KEY` and a proxy
are required (see [DataDome](#datadome)). `login` uses the browser OAuth flow
once and stores the tokens in the config dir; everything else runs headless.

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
  -e TWOCAPTCHA_API_KEY="$TWOCAPTCHA_API_KEY" \
  -v "$CFG":/data/intermarche-mcp \
  ghcr.io/nicolasguilloux/intermarche-mcp:latest login

# one-shot command
docker run --rm \
  -e TWOCAPTCHA_API_KEY="$TWOCAPTCHA_API_KEY" \
  -v "$CFG":/data/intermarche-mcp \
  ghcr.io/nicolasguilloux/intermarche-mcp:latest search "lait demi écrémé"

# MCP server (no args → `mcp http :8080`)
docker run --rm -p 8080:8080 \
  -e TWOCAPTCHA_API_KEY="$TWOCAPTCHA_API_KEY" \
  -v "$CFG":/data/intermarche-mcp \
  ghcr.io/nicolasguilloux/intermarche-mcp:latest
```

The image is a static binary on Alpine (~24 MB) — no browser, no system deps.
Prefer building locally? `docker build -t intermarche-mcp:latest -f docker/Dockerfile .`

### C. Docker Compose

```bash
cp .env.example .env        # fill TWOCAPTCHA_API_KEY (+ IMT_CONFIG_DIR)
docker compose run --rm intermarche login               # browser OAuth flow, once
docker compose up --build                              # MCP server on :8080
docker compose run --rm intermarche search "lait demi écrémé"
docker compose run --rm intermarche orders
```

Set `IMT_CONFIG_DIR` in `.env` to reuse a host session; leave it empty for a
self-contained named volume (then set `CAPTCHA_PROXY`). Full details in
[`docker/README.md`](docker/README.md).

## Configuration

Configured entirely through environment variables (see [`.env.example`](.env.example)):

| Variable | Used by | Purpose |
| --- | --- | --- |
| `TWOCAPTCHA_API_KEY` | solver | 2Captcha credit pool. Required (and so is a proxy). |
| `IMT_CONFIG_DIR` | compose | Host config dir to reuse inside the container (tokens + DataDome cookie). |
| `CAPTCHA_PROXY` | solver | Standing proxy for challenge fetch + API calls, also handed to 2Captcha's workers. Alias: `IMT_PROXY`. |
| `CAPTCHA_PROXY_LISTEN` | solver | Local bind address of the single-use proxy served during a solve. Excludes `CAPTCHA_PROXY`. |
| `CAPTCHA_PROXY_ADVERTISE` | solver | Public `host:port` reaching that listener — what 2Captcha is told to use. `ngrok` reads it from a running agent instead. |
| `CAPTCHA_PROXY_ALLOW` | solver | Domains the single-use proxy may tunnel to (default `intermarche.com,captcha-delivery.com,ident.me,tnedi.me`). |
| `NGROK_AUTHTOKEN` | ngrok agent | Credential for `ngrok tcp`, read by ngrok itself. Only needed with `CAPTCHA_PROXY_ADVERTISE=ngrok`. |
| `NGROK_REGION` | ngrok agent | Edge region for the tunnel: `us` (default), `eu`, `ap`, `au`, `sa`, `jp`, `in`. |
| `CAPTCHA_MAX_SOLVES` | solver | Cap on paid solves per run (default 1). |
| `IMT_USER_AGENT` | solver | Override the Chrome User-Agent sent with requests. |

State (OAuth tokens, selected store, cached DataDome cookie) lives in the config
dir — `~/.config/intermarche-mcp` on Linux, `~/Library/Application Support/intermarche-mcp`
on macOS, or `/data/intermarche-mcp` in the container.

## DataDome

intermarche.com is protected by DataDome. Every API call is wrapped through a
captcha **solver** that clears the challenge over the network (no browser).
A solve is only ever triggered on an actual `403` and is **paid**, so the
cleared cookie is cached on disk and the steady state costs zero credits
(capped by `CAPTCHA_MAX_SOLVES`, default 1).

The solver is **2Captcha**:

```env
TWOCAPTCHA_API_KEY=your-key         # https://2captcha.com
CAPTCHA_PROXY=http://user:pass@host:port   # REQUIRED
```

> [!NOTE]
> **Salamoonder was removed in August 2026.** It used to be the default
> provider, but its slider solver never cleared intermarche.com: with a correct
> captcha URL and a correct IP binding, every task came back with a `DD_…`
> support code and no cookie. Since it could not do the one job it was there
> for, keeping it meant shipping a default that fails.
>
> `CAPTCHA_PROVIDER=salamoonder` is now rejected at startup, and
> `SALAMOONDER_API_KEY`, `SALAMOONDER_PROXY` and `SALAMOONDER_MAX_SOLVES` are no
> longer read. Upgrading from an older `.env`: set `TWOCAPTCHA_API_KEY`, add a
> proxy (see below), and drop the `SALAMOONDER_*` lines — `CAPTCHA_PROVIDER`
> itself can simply go, `2captcha` is the default and the only value.

The steps are split: *we* fetch the challenge (minting the `cid` on our IP)
while 2Captcha's workers solve the slider from their own IPs. For the returned
cookie to be valid, both must share one IP — so a proxy is **mandatory**: it is
handed to 2Captcha so its workers replay the solve through the same egress that
minted the `cid`. Without one the solver refuses to start.

Here is the whole exchange, in the single-use proxy + ngrok shape:

```mermaid
sequenceDiagram
    autonumber
    participant CLI as intermarche-mcp
    participant MP as single-use proxy
    participant NG as ngrok agent + edge
    participant TC as 2Captcha
    participant DD as intermarche.com + DataDome

    CLI->>DD: API call with the cached cookie
    DD-->>CLI: 403 + challenge
    CLI->>DD: fetch the challenge — mints the cid on this host's IP
    DD-->>CLI: slider URL

    CLI->>NG: read the tunnel address from the local agent
    CLI->>MP: start — credentials generated for this solve only
    CLI->>TC: createTask: slider URL, UA, tunnel address + credentials

    TC->>NG: CONNECT to the advertised address
    NG->>MP: forward to the local listener
    MP->>DD: tunnel the solve — same egress IP as the CLI
    Note over MP: anything outside the allow list is refused
    TC-->>CLI: cleared datadome cookie

    CLI->>MP: close — listener and credentials destroyed
    CLI->>DD: retry with the cookie
    DD-->>CLI: 200
```

The point of the whole dance is step 10: DataDome ties the clearance to the IP
that minted the `cid`, so the challenge fetch, the workers' solve and the
subsequent API calls must all leave from the same address. ngrok only carries
the **inbound** leg — the tunnel lets 2Captcha in, but the connection to
intermarche.com is still opened by this host.

#### Single-use proxy (instead of a standing one)

If you would rather not keep a proxy running, this process can serve one for
the length of a solve and destroy it right after:

```env
CAPTCHA_PROVIDER=2captcha
TWOCAPTCHA_API_KEY=your-key
CAPTCHA_PROXY_LISTEN=0.0.0.0:18888        # local bind
CAPTCHA_PROXY_ADVERTISE=203.0.113.7:18888 # public host:port that reaches it
```

The listener exists only between `CreateTask` and the solve result, its
credentials are generated for that one solve, and `CONNECT` is refused for
anything outside `CAPTCHA_PROXY_ALLOW` (`intermarche.com` and
`captcha-delivery.com` by default, subdomains included) or outside ports 80/443.
Targets resolving to a private or loopback address are refused too, so an
allowed domain cannot be pointed back at your network. Every refusal is logged
with its source IP.

Routing the public address to the listener is **your** job — port forward,
tunnel, firewall rule. With ngrok, whose address changes on every restart, set
`CAPTCHA_PROXY_ADVERTISE=ngrok` and it is read from the agent
(`127.0.0.1:4040`) at each solve:

```bash
set -a; . ./.env; set +a   # exports NGROK_AUTHTOKEN, among others
ngrok tcp 18888            # then CAPTCHA_PROXY_ADVERTISE=ngrok
```

`NGROK_AUTHTOKEN` and `NGROK_REGION` are read by the ngrok agent itself, so
keeping them in `.env` avoids a machine-wide `ngrok.yml` and travels with the
rest of the config. The region only changes tunnel latency: the solve still
egresses from this host, which is the IP DataDome binds the clearance to.

Note that an ngrok endpoint is public and cannot be restricted by source IP on
the free plan, so the single-use credentials and the domain allow list are all
that stand in front of it — and every connection reaches the proxy from the
agent's loopback, so the refusal logs no longer show the caller's real IP. Note that 2Captcha's workers can only speak plaintext
`CONNECT`, so this port cannot be wrapped in TLS: keep it firewalled to
2Captcha's egress (`138.201.188.166`, per their docs) if you can.

`CAPTCHA_PROXY_LISTEN` and `CAPTCHA_PROXY` are mutually exclusive: with the
single-use proxy the API calls egress **directly**, so that both legs share
this host's IP.

### IP binding (both providers)

DataDome binds the clearance to the IP context of the challenge, so the
challenge fetch, the solve **and** the subsequent API calls should all egress
from the same IP. `CAPTCHA_PROXY` (when set) is used for *both* the challenge
fetch and every API call, keeping them aligned. A cookie minted *inside* a
container can be rejected when the egress differs; reuse a host session
(`IMT_CONFIG_DIR`) or set a residential `CAPTCHA_PROXY` — details in
[`docker/README.md`](docker/README.md).

| Provider | API key | Proxy |
| --- | --- | --- |
| `2captcha` | `TWOCAPTCHA_API_KEY` | **required**: `CAPTCHA_PROXY`, or `CAPTCHA_PROXY_LISTEN` + `CAPTCHA_PROXY_ADVERTISE` |

Authentication uses the browser OAuth flow (`intermarche-mcp login`); the
`desktop` Keycloak client forbids password (ROPC) login.

## Docs

- [`docker/README.md`](docker/README.md) — Docker specifics, DataDome IP-binding, session reuse
- [`docs/api-reference.md`](docs/api-reference.md) — API surface
- [`docs/add-to-basket-flow.md`](docs/add-to-basket-flow.md) — cart flow
- [`docs/authentication.md`](docs/authentication.md) — auth flow
