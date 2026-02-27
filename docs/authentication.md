# Authentication

## Overview

Intermarché uses **Keycloak** as its identity provider, exposed as a standard
**OpenID Connect** server at:

```
https://itmconnect.intermarche.com/auth/realms/customers
```

Well-known config:
```
https://itmconnect.intermarche.com/auth/realms/customers/.well-known/openid-configuration
```

## Login Methods

The website login overlay offers three options:
1. **Google** social login
2. **Apple** social login
3. **Email** login (Keycloak native)

## OIDC Endpoints

| Endpoint | URL |
|----------|-----|
| Authorization | `https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/auth` |
| Token | `https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/token` |
| UserInfo | `https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/userinfo` |
| Logout | `https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/logout` |
| Device Auth | `https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/auth/device` |
| JWKS | `https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/certs` |
| Revoke | `https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/revoke` |

## Web Authorization Code Flow (PKCE)

The website uses **Authorization Code + PKCE (S256)**:

```
GET https://itmconnect.intermarche.com/auth/realms/customers/protocol/openid-connect/auth
  ?response_type=code
  &client_id=desktop
  &code_challenge_method=S256
  &code_challenge=<base64url-encoded SHA256 of code_verifier>
  &redirect_uri=https://www.intermarche.com/api/connexion
  &layout=ITM
  &kc_idp_hint=google|apple   (optional, for social login)
```

- **client_id**: `desktop` (from env var `AUTH_CLIENT_ID_SOCIAL`)
- **redirect_uri**: `https://www.intermarche.com/api/connexion`
- **PKCE**: code_verifier is generated client-side, SHA256 hashed for the challenge
- The callback at `/api/connexion` exchanges the authorization code for tokens

For email login, the user is first sent to `/connexion/itm?redirect=<url>` which
builds the same authorize URL but lands on Keycloak's native login form.

## Supported Grant Types

| Grant Type | Use Case |
|------------|----------|
| `authorization_code` | Primary web flow (with PKCE) |
| `password` | Resource Owner Password Credentials (direct email+password) |
| `refresh_token` | Refresh expired access tokens |
| `client_credentials` | Service-to-service |
| `urn:ietf:params:oauth:grant-type:device_code` | **Device authorization** (ideal for CLI) |

## CLI Authentication Strategy

The Keycloak server supports the **Device Authorization Grant** (RFC 8628), which is the
recommended flow for CLI applications:

1. CLI requests a device code from the device authorization endpoint
2. CLI displays a URL and user code to the terminal
3. User opens the URL in their browser and enters the code
4. CLI polls the token endpoint until the user completes authentication
5. CLI receives access_token + refresh_token

Alternatively, since the `password` grant type is supported, the CLI could directly
accept email + password and exchange them for tokens (simpler but less secure — no MFA
support, credentials handled by the CLI).

## Scopes

Available scopes:
- `openid` — required for OIDC
- `email` — user's email
- `profile` — name, given_name, family_name, preferred_username
- `phone` — phone number
- `address` — postal address
- `offline_access` — get a refresh_token for long-lived sessions
- `roles` — user roles
- `basic` — basic profile info

## Claims

The ID token / userinfo endpoint returns:
`aud`, `sub`, `iss`, `auth_time`, `name`, `given_name`, `family_name`,
`preferred_username`, `email`, `acr`

## Token Usage

Once authenticated, the access token is used via:
- `x-oauth: true` header on proxied API calls
- `customerId` parameter (the Keycloak `sub` claim) for cart and user-specific endpoints
- `itm_session` header for shopping list and consumer APIs

## Session Storage

The frontend stores the OIDC config in `sessionStorage` under the key `CONFIG`.
Auth state is persisted in `localStorage` under `persist:auth`:
```json
{
  "sessionExpired": "false",
  "isHydrated": "true",
  "_persist": "{\"version\":-1,\"rehydrated\":true}"
}
```

## Cookies

Auth-related cookies:
- `itm_device_id` — device UUID + active status
- `RED_ACTUAL_URI` — redirect URI saved before auth redirect
- `bm_sv` — Datadome bot management session
- `datadome` — Datadome cookie
