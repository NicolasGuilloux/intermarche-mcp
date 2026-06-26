#!/bin/sh
# Browser-free entrypoint for the Salamoonder ("solver") transport.
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

CAPTCHA_PROVIDER="${CAPTCHA_PROVIDER:-salamoonder}"
case "${CAPTCHA_PROVIDER}" in
    2captcha|twocaptcha)
        if [ -z "${TWOCAPTCHA_API_KEY}" ]; then
            echo "WARN: TWOCAPTCHA_API_KEY is empty — the solver will error on the first DataDome 403." >&2
        fi
        if [ -z "${CAPTCHA_PROXY}" ] && [ -z "${SALAMOONDER_PROXY}" ] && [ -z "${IMT_PROXY}" ]; then
            echo "WARN: 2captcha requires a proxy (CAPTCHA_PROXY) — the solver will error on the first DataDome 403." >&2
        fi
        ;;
    *)
        if [ -z "${SALAMOONDER_API_KEY}" ]; then
            echo "WARN: SALAMOONDER_API_KEY is empty — the solver will error on the first DataDome 403." >&2
        fi
        ;;
esac

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
