# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

CLI tool for interacting with the Intermarché French grocery store. Provides commands to browse products, manage baskets, and view order history by talking to the intermarche.com API.

## Development Environment

Uses **Nix devenv** for tooling (Go, SQLite, gcc). After cloning, `direnv allow` or `devenv shell` sets everything up.

## Build & Test

All Go code lives under `src/` (the module root). Run Go commands from there:

```bash
cd src && go build -o ../intermarche-mcp .   # build
cd src && go test ./...                      # run all tests
cd src && go test ./pkg/cart -run TestAdd    # run a single test
```

## Architecture

- **Language**: Go (module: `github.com/nover/intermarche-mcp`)
- **Local state**: JSON files in the config dir (`tokens.json`, `store.json`,
  `datadome.json`) — `~/.config/intermarche-mcp` (Linux) /
  `~/Library/Application Support/intermarche-mcp` (macOS), overridable with
  `XDG_CONFIG_HOME`.
- **Entry point**: `main.go` — subcommand dispatch (`orders`, `search`, `basket`)

## API Interaction

The Intermarché website uses Datadome anti-bot protection. Every API call is
wrapped through a single transport — the **captcha solver**
(`internal/solver`) — which clears Datadome over the network. There is no
browser anywhere at runtime.

- Solves the Datadome slider through **2Captcha** (key in
  `TWOCAPTCHA_API_KEY`), then calls the API with pure Go `net/http`: **HTTP/1.1
  is forced** (Go's default HTTP/2 fingerprint is flagged) and a **full Chrome
  header set** + the device fingerprint (from `itm_device_id`) are sent.
- The cleared cookie is cached on disk (`<config>/datadome.json`) and reused; a
  credit is only spent on an actual 403. Capped by `CAPTCHA_MAX_SOLVES`
  (default 1) so a broken-cookie loop can't drain the balance.
- **IP binding (important):** Datadome binds the clearance to the IP context of
  the challenge, so the challenge fetch AND the API calls must egress from the
  same IP — and so must 2Captcha's workers, which replay the solve. Set a
  proxy via `CAPTCHA_PROXY` (deprecated alias `IMT_PROXY`),
  e.g. `http://user:pass@host:port` — used for both. A cookie minted from one IP
  context (e.g. inside a container) can be rejected when used from another; see
  `docker/README.md`.
- **Single-use proxy:** instead of a standing proxy, `internal/solver/microproxy`
  serves a CONNECT proxy for the length of one solve — fresh credentials per
  solve, domain allow list (incl. `ident.me`/`tnedi.me`, which the workers use to
  check the IP they were given), listener destroyed on return. Mutually
  exclusive with `CAPTCHA_PROXY`. Two ways to expose it, via
  `CAPTCHA_PROXY_ADVERTISE`:
  - `ngrok` — `internal/solver/ngroktunnel.go` opens a TCP tunnel through the
    ngrok Go SDK when the solve starts and closes it with the proxy
    (`NGROK_AUTHTOKEN` only; no binary, works in the container). Careful: the
    context passed to `agent.Listen` owns the endpoint's lifetime, so cancelling
    it kills the public address — it is cancelled in `Close`, and setup is
    bounded separately.
  - `host:port` (or `ngrok:<agent-api>`) with `CAPTCHA_PROXY_LISTEN` — the
    operator exposes the port.

All API calls are proxied through `/api/service{path}` on `www.intermarche.com`. The actual backend microservices live behind this proxy:
- `/panier/v1/stores/{storeId}/carts` — cart operations (sync-based: full state + events)
- `/consommateur/v1/consommateurs/{userId}/...` — shopping lists, favorites, orders
- `/tunnelachat/v1/tunnels/{userId}/deliveries` — delivery slots

A store (PDV / `pdvRef`) must be selected before any product or cart operations work. Products are identified by EAN barcodes.

Full API docs in `docs/api-reference.md`, cart flow details in `docs/add-to-basket-flow.md`.
