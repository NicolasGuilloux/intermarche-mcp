#!/bin/sh
# Browser-free entrypoint for the captcha ("solver") transport.
#
# Boot sequence:
#   1. sanity-check the solver credential,
#   2. log in (ROPC, no browser) if EMAIL/PASSWORD are provided,
#   3. exec the requested command (default: the MCP HTTP server).
#
# Store selection is handled at runtime through the MCP server, not here.
#
# Any extra arguments passed to the container override step 3, e.g.
#   docker compose run --rm intermarche search "lait demi écrémé"
set -e

if [ -z "${TWOCAPTCHA_API_KEY}" ]; then
    echo "WARN: TWOCAPTCHA_API_KEY is empty — the solver will error on the first DataDome 403." >&2
fi
if [ -z "${CAPTCHA_PROXY}" ] && [ -z "${IMT_PROXY}" ] && [ -z "${CAPTCHA_PROXY_ADVERTISE}" ]; then
    echo "WARN: 2captcha requires a proxy — set CAPTCHA_PROXY, or CAPTCHA_PROXY_ADVERTISE for a single-use one — the solver will error on the first DataDome 403." >&2
fi
if [ -n "${CAPTCHA_PROXY_LISTEN}" ] && [ -z "${CAPTCHA_PROXY_ADVERTISE}" ]; then
    echo "WARN: CAPTCHA_PROXY_LISTEN is set without CAPTCHA_PROXY_ADVERTISE — the solver will refuse to start." >&2
fi
if [ "${CAPTCHA_PROXY_ADVERTISE}" = "ngrok" ] && [ -z "${NGROK_AUTHTOKEN}" ]; then
    echo "WARN: CAPTCHA_PROXY_ADVERTISE=ngrok needs NGROK_AUTHTOKEN — no tunnel can be opened on the first DataDome 403." >&2
fi

# ── Authentication ─────────────────────────────────────────────────────────────
# Auth tokens are read from / written to the mounted /data volume, so logging in
# once persists across restarts. Two ways to get a session into the volume:
#
#   a) Browser flow (the only one the "desktop" Keycloak client allows):
#        run `intermarche-mcp login` on a host with a browser, then make its
#        config dir available to the container — either bind-mount it onto
#        /data, or copy tokens.json into the imt-config volume.
#
#   b) Password grant (ROPC): only works if your Keycloak client permits direct
#        access grants. The default "desktop" client does NOT, so this errors
#        with "Client not allowed for direct access grants" — set EMAIL/PASSWORD
#        only if you know your client allows it. Tried here, non-fatally.
#
# Unauthenticated commands (store, search) work without any of this.
if [ -n "${EMAIL}" ] && [ -n "${PASSWORD}" ]; then
    echo "Attempting password-grant login as ${EMAIL}..."
    intermarche-mcp login-password "${EMAIL}" "${PASSWORD}" || \
        echo "WARN: password login rejected — the 'desktop' client forbids it. Use the browser flow and persist tokens into /data (see README). Auth-only commands (orders, basket) will fail until then." >&2
fi

# ── Command (default: MCP HTTP server) ─────────────────────────────────────────
if [ "$#" -eq 0 ]; then
    set -- mcp http "${MCP_ADDR:-:8080}"
fi

exec intermarche-mcp "$@"
