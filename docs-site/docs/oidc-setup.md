# Connect an external identity provider (OIDC)

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - Where the IdP configuration actually lives (per-tenant, in the database — not environment variables)
    - The five fields you gather from your IdP and where each one lands
    - How to validate the issuer with a server-side preflight before saving
    - Per-IdP notes for Okta, Entra ID (Azure AD), and Keycloak
    - The security rules that keep the redirect URI and client secret safe
<!-- prettier-ignore-end -->

security-atlas is an **OIDC relying party** — it authenticates a human
against an external identity provider and then mints its own atlas token.
It is never an identity provider itself. The architecture (RP authenticates
the human; the atlas authorization server mints the atlas JWT) is described
in [ADR-0003](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0003-oauth-authorization-server.md).

This guide is for an operator connecting an IdP — Okta, Entra ID (Azure AD),
or Keycloak — to a **running** deployment. If you have not brought the
platform up yet, start with the [install guide](install.md): first sign-in
is **local mode** and needs no IdP. Connecting an IdP is an additive step
you take later (see [Local mode stays available](#local-mode-stays-available)).

## Where the configuration lives

The IdP configuration is **per tenant** and stored **in the database**, in
the `oidc_idp_configs` table. You set it through the admin SSO surface:

- the web UI at **`/admin/sso`**, or
- the API: **`PATCH /v1/admin/sso`**.

Both require an **admin** credential for the tenant you are configuring.

<!-- prettier-ignore-start -->
!!! warning "There is no `OIDC_ISSUER_URL` environment variable"

    The IdP issuer, client ID, client secret, and redirect URI are **not**
    environment variables and do **not** belong in `.env`,
    `docker-compose.yml`, or any committed file. They are tenant-scoped
    database rows written through the admin surface above. (The unrelated
    `ATLAS_ISSUER_URL` env var sets the atlas authorization server's own
    issuer identity — a different value; do not confuse the two.)
<!-- prettier-ignore-end -->

The platform surfaces **one primary IdP per tenant** (the row is keyed by
`name = "primary"`). Multi-IdP federation, SCIM provisioning, and
group-to-role mapping are not part of this surface today — see
[Out of scope](#out-of-scope-future-work).

## The five fields you gather

You collect five values from your IdP. Each maps to a field on the admin SSO
request and a column on `oidc_idp_configs`:

| You gather            | Admin SSO field (`PATCH /v1/admin/sso`) | `oidc_idp_configs` column | Notes                                                             |
| --------------------- | --------------------------------------- | ------------------------- | ----------------------------------------------------------------- |
| Issuer URL            | `issuer_url`                            | `issuer_url`              | The base from which `/.well-known/openid-configuration` resolves. |
| Client ID             | `client_id`                             | `client_id`               | The opaque identifier the IdP issues for the atlas application.   |
| Client secret         | `client_secret`                         | `client_secret_enc`       | Write-only; stored encrypted at rest; never returned by a read.   |
| Redirect URI          | `redirect_url`                          | `redirect_url`            | Exact-match, own-origin, atlas callback path (see below).         |
| Allowed email domains | `allowed_email_domains`                 | `allowed_email_domains`   | List of domains permitted to sign in; the principal restriction.  |

`GET /v1/admin/sso` returns the saved configuration **without** the client
secret — the secret is write-only and is never read back onto the wire.

### The redirect URI

The redirect URI is the single most security-sensitive value here. The
platform's OAuth callback handler is registered at exactly one path:

```text
/auth/oidc/callback
```

So the redirect URI you register at the IdP and store in `redirect_url` must
be your deployment's own origin followed by that path, for example:

```text
https://atlas.example.com/auth/oidc/callback
```

<!-- prettier-ignore-start -->
!!! danger "Redirect URI: exact-match, own-origin, no wildcards"

    Register the redirect URI at your IdP as an **exact string match** to
    the value above. Do **not** use a wildcard (`https://atlas.example.com/*`),
    do **not** point it at any host other than your own deployment, and do
    **not** add a foreign or attacker-controllable host to the IdP's allowed
    redirect list. A permissive redirect URI is an open-redirect / token-
    interception surface; exact-match own-origin closes it. The callback
    path is fixed at `/auth/oidc/callback` — the only legitimate redirect
    target.
<!-- prettier-ignore-end -->

### The client secret

The client secret is the sensitive credential in this flow.

<!-- prettier-ignore-start -->
!!! danger "Never commit or log the client secret"

    Enter the client secret **only** through the admin surface (the
    `/admin/sso` form's write-only field, or the `client_secret` field of
    `PATCH /v1/admin/sso`). It is stored **encrypted at rest** in
    `client_secret_enc` and is never returned by `GET /v1/admin/sso`. Do
    **not** place it in `.env`, `docker-compose.yml`, a Helm `values.yaml`,
    any committed file, or any log line. The admin form's secret field is a
    write-only password input; leaving it **blank** on a later save keeps the
    existing secret unchanged.
<!-- prettier-ignore-end -->

### Allowed email domains

`allowed_email_domains` is the principal restriction: which IdP-authenticated
users may actually sign in. When the list is non-empty, the relying party
admits a user only if their verified email ends with `@<domain>` for one of
the listed domains (case-insensitive). When the list is empty, **any** user
your IdP authenticates is admitted.

<!-- prettier-ignore-start -->
!!! tip "Set `allowed_email_domains`"

    Set this to the domain(s) your workforce uses (for example
    `example.com`). Leaving it empty means every account your IdP can
    authenticate — including guest or consumer accounts in a multi-tenant
    IdP — can sign in to your deployment. The admin form takes a
    comma-separated list; the API takes a JSON array of strings.
<!-- prettier-ignore-end -->

## Step 1 — preflight the issuer

Before saving, validate that the issuer is reachable and serves a discovery
document. The admin surface exposes a server-side check:

```text
POST /v1/admin/sso/preflight
{ "issuer_url": "https://idp.example.com" }
```

The server fetches `https://idp.example.com/.well-known/openid-configuration`
and returns the endpoints it parsed:

```json
{
  "issuer": "https://idp.example.com",
  "authorization_endpoint": "https://idp.example.com/authorize",
  "token_endpoint": "https://idp.example.com/token",
  "jwks_uri": "https://idp.example.com/jwks"
}
```

The same check is available as a button on the `/admin/sso` page ("Run
preflight").

<!-- prettier-ignore-start -->
!!! note "Preflight constraints"

    The preflight fetch requires an **`https`** issuer and rejects raw IP
    addresses and hosts that resolve to loopback or private (RFC1918) ranges
    — an SSRF defense. The issuer must therefore be reachable from the atlas
    host over the public network (or your deployment's resolvable internal
    DNS, as long as it is not a private-range address). The response body is
    size-capped and the request times out after a few seconds.
<!-- prettier-ignore-end -->

## Step 2 — register the application at your IdP

Create an OIDC application (web / confidential client) at your IdP, set the
redirect URI to `https://<your-deployment>/auth/oidc/callback`, and collect
the issuer URL, client ID, and client secret. The atlas relying party
requests the `openid`, `email`, and `profile` scopes, so make sure the
application is allowed to release them.

Per-IdP specifics follow.

=== "Okta"

    - **Application type:** OIDC → Web Application.
    - **Sign-in redirect URI:** `https://<your-deployment>/auth/oidc/callback`
      (exact match).
    - **Issuer URL:** the Okta org URL `https://<your-org>.okta.com`, or, if
      you use a custom authorization server, the per-server form
      `https://<your-org>.okta.com/oauth2/<auth-server-id>`. Confirm with
      preflight — the issuer in the discovery document must match.
    - **Client ID / client secret:** shown on the application's **General**
      tab after creation. The secret is the value you paste into the
      write-only secret field.
    - **Allowed grant types:** Authorization Code (the platform uses code +
      PKCE).

=== "Entra ID (Azure AD)"

    - **Register** an application under **Microsoft Entra ID → App
      registrations**.
    - **Redirect URI:** platform type **Web**, value
      `https://<your-deployment>/auth/oidc/callback` (exact match).
    - **Issuer URL:** the v2.0 issuer
      `https://login.microsoftonline.com/<tenant-id>/v2.0` (the discovery
      document lives at `.../v2.0/.well-known/openid-configuration`). Use
      preflight to confirm.
    - **Client ID:** the **Application (client) ID** on the app's Overview
      page.
    - **Client secret:** create one under **Certificates & secrets → Client
      secrets**; copy the secret **value** (not the secret ID) immediately,
      as it is shown only once.

=== "Keycloak"

    - **Create a client** in your realm with **Client authentication** on
      (a confidential client).
    - **Valid redirect URIs:** `https://<your-deployment>/auth/oidc/callback`
      (exact match — avoid the trailing `*` Keycloak offers as a convenience).
    - **Issuer URL:** the realm URL `https://<keycloak-host>/realms/<realm>`
      (discovery at `.../realms/<realm>/.well-known/openid-configuration`).
      Confirm with preflight.
    - **Client ID:** the client ID you chose on creation.
    - **Client secret:** the **Credentials** tab of the client, once **Client
      authentication** is enabled.

## Step 3 — save the configuration

Enter the five values through the `/admin/sso` form (or `PATCH
/v1/admin/sso`) and save. On the first save the client secret is required;
on later saves you may leave the secret field blank to keep the stored
secret unchanged.

A `PATCH /v1/admin/sso` body looks like this (all values are placeholders —
substitute your own):

```json
{
  "issuer_url": "https://idp.example.com",
  "client_id": "atlas-client-id-placeholder",
  "client_secret": "REPLACE-WITH-YOUR-IDP-CLIENT-SECRET",
  "redirect_url": "https://atlas.example.com/auth/oidc/callback",
  "allowed_email_domains": ["example.com"]
}
```

## Step 4 — sign in through the IdP

With a primary IdP configured, the relying-party flow runs at:

```text
/auth/oidc/login?tenant_id=<tenant-id>&idp=primary
```

which redirects the user to your IdP and returns them to
`/auth/oidc/callback`. The flow uses **state** (CSRF defense), **PKCE**
(code-interception defense), and a per-flow **nonce** (ID-token-replay
defense) — all enforced by the relying party. A user whose email is outside
`allowed_email_domains` is rejected at the callback.

OIDC sign-ins are recorded by the existing audit surface, which distinguishes
a state/CSRF mismatch from an ID-token-replay attempt. This guide does not
add a new audit surface.

## Local mode stays available

Connecting an IdP is **additive**. The local default user created at first
boot (the `ATLAS_DEFAULT_USER_EMAIL` / `ATLAS_DEFAULT_USER_PASSWORD` account
from the [install guide](install.md)) continues to work after you configure
OIDC. Configuring an IdP does not disable local sign-in unless you choose to
remove the local account yourself.

## Out of scope (future work)

These are not part of the shipped admin SSO surface today and are noted here
only so expectations are clear:

- **Multi-IdP federation** — the surface manages one primary IdP per tenant.
- **SCIM provisioning** — users are provisioned just-in-time at first OIDC
  sign-in, not pushed from the IdP.
- **Group-to-role mapping** — IdP group claims are not mapped to atlas roles;
  roles are managed inside atlas.

## See also

- [Install guide](install.md) — local-mode first boot.
- [ADR-0003 — OAuth authorization server](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0003-oauth-authorization-server.md)
  — the relying-party plus authorization-server architecture.
