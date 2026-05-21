# Migrating from API keys to OAuth (slice 191)

Slice 191 retires the slice 034 bearer-token middleware. Every
`/v1/*` request now authenticates via the OAuth 2.0 Authorization
Server's JWT access tokens (machines) or via the slice 189 OIDC
sign-in flow (humans in the browser).

If you are reading this because a CLI / SDK call returned **410
Gone** with body `{"error":"api_key_deprecated"}`, this doc is
your migration runbook.

## What changed

Before slice 191:

- SDK consumers presented a slice-034-issued bearer token directly:
  `Authorization: Bearer atlas_<32-char-base32>`.
- The platform looked up the token's hash in `api_keys`, set the
  request's tenant + roles, and processed `/v1/*` requests.

After slice 191:

- Slice 034's bearer-token middleware is removed from `internal/api/httpserver.go`.
- `/v1/*` requests carrying a legacy `atlas_...` bearer return
  **410 Gone** with a `migration_url` field in the response body.
- The authoritative auth path is the slice 190 JWT validation
  middleware. SDKs acquire JWTs from `POST /oauth/token`; CLI
  users acquire JWTs from the device-code flow.

The wire protocol on `/v1/*` is **unchanged** — it is still
`Authorization: Bearer <token>`. Only the token-acquisition flow
moves.

## Audience: SDK consumers (machines)

For any service that uses an api-key today, switch to the OAuth
`client_credentials` grant.

### Step 1 — issue an OAuth client mirroring the api-key identity

The operator (you) generates a NEW OAuth client identity that
inherits the api-key's name. Run on a host with `DATABASE_URL` in
the environment:

```bash
DATABASE_URL=postgres://... \
  atlas-cli oauth migrate-api-key atlas_<your-legacy-token>
```

Output:

```
source: id=key_abcdef tenant=11111111-... admin=false approver=false roles=[]
client_id: 0e3b4a7e-4ad8-4c14-9c9a-6f8e2b13c92d
client_secret: A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0
```

Record the `client_secret` IMMEDIATELY — it is unrecoverable.
Treat it as a credential of the same sensitivity as the legacy
api-key it replaces.

### Step 2 — update your SDK call sites

#### Go

```go
import "github.com/mgoodric/security-atlas/pkg/sdk-go/oauth"

oc, err := oauth.NewClient(oauth.Config{
    ClientID:     "0e3b4a7e-4ad8-4c14-9c9a-6f8e2b13c92d",
    ClientSecret: "A1b2C3d4...",
    IssuerURL:    "https://atlas.example.com",
})
if err != nil { ... }

token, err := oc.Token(ctx)
if err != nil { ... }

// Use token as the Authorization: Bearer for any /v1/* call.
req.Header.Set("Authorization", "Bearer "+token)
```

The Go SDK's `Client` caches the token and refreshes it 60s
before expiry. It is safe for concurrent callers.

#### Python

```python
from pyatlas import OAuthClient

oc = OAuthClient(
    client_id="0e3b4a7e-4ad8-4c14-9c9a-6f8e2b13c92d",
    client_secret="A1b2C3d4...",
    issuer_url="https://atlas.example.com",
)
token = oc.token()
```

#### TypeScript

```typescript
import { OAuthClient } from "@security-atlas/sdk";

const oc = new OAuthClient({
  clientId: "0e3b4a7e-4ad8-4c14-9c9a-6f8e2b13c92d",
  clientSecret: "A1b2C3d4...",
  issuerUrl: "https://atlas.example.com",
});
const token = await oc.getToken();
```

### Step 3 — revoke the legacy api-key

Once the new OAuth credentials are working in production:

```bash
atlas-cli credentials revoke key_abcdef
```

The migration tool **does not** revoke the source key automatically
— that step is yours so you can validate the new credentials
without a flag-day cutover.

### Java SDK

The Java SDK is filed as a spillover slice (#195). If you need
Java today, use the `client_credentials` grant directly with any
OAuth library that supports RFC 6749 §4.4:

```
POST <issuer>/oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials&client_id=...&client_secret=...
```

The response is RFC 6749 §5.1; cache the `access_token` until
60 seconds before `expires_in`.

## Audience: human CLI users

The atlas CLI now ships with an `atlas login` command that
implements the RFC 8628 Device Authorization Grant. You no longer
hold a long-lived api-key on disk — the CLI acquires a 1-hour
JWT via your browser.

```bash
atlas login --issuer https://atlas.example.com --client-id <cli-client-id>
```

The CLI prints a URL + short code:

```
Visit https://atlas.example.com/oauth/device?user_code=ABCD-2345
  (or go to https://atlas.example.com/oauth/device and enter code ABCD-2345)
Waiting for approval (up to 900 seconds)...
```

Open the URL in a browser, sign in via your IdP if not already
signed in, and click **Approve**. The CLI's polling loop detects
the approval and writes the resulting JWT to
`~/.config/atlas/credentials.json` (mode 0600). Subsequent CLI
commands present the JWT automatically.

Re-run `atlas login` whenever the JWT expires (1 hour by default;
the CLI surfaces an expired-token error on its next call).

### Operator setup: register a CLI client_id

Before any user can run `atlas login`, the operator issues a
public OAuth client for the CLI:

```bash
DATABASE_URL=postgres://... \
  atlas-cli oauth issue-client cli-public
client_id: 0e3b4a7e-...
client_secret: A1b2C3d4...
```

The CLI uses **only** the `client_id` (the device-code flow does
not require a `client_secret` for public clients per RFC 8628
§5.1; the secret is unused — it can be discarded). Distribute
the `client_id` to your CLI users via a wiki / configuration
management.

## Audience: operators

### What you need to do

1. **Roll out slice 191 to your atlas deployment.** Existing
   legacy api-keys will start returning 410 Gone after the
   deployment.
2. **Issue an OAuth client for the CLI** (`atlas-cli oauth issue-client cli-public`).
3. **Migrate each existing api-key** with
   `atlas-cli oauth migrate-api-key`.
4. **Notify your CLI users** to run `atlas login` once with the
   `--client-id` value from step 2.
5. **After confirming everyone has migrated**, revoke the legacy
   api-keys (`atlas-cli credentials revoke`).

### What does NOT change

- Tenant scoping: OAuth JWTs carry `atlas:current_tenant_id` +
  `atlas:available_tenants[]` claims that the slice 190 JWT
  middleware uses to set the `app.current_tenant` Postgres GUC.
  RLS continues to enforce tenant isolation at the DB layer.
- Role-based access control: each JWT carries the user's
  per-tenant role map in `atlas:roles`. The slice 035 OPA engine
  reads those roles.
- Audit logging: every token mint writes a row to
  `oauth_token_exchanges`; every `/oauth/revoke` writes to
  `oauth_revocation_events`. Both tables are append-only.

## What's deferred (v3)

Slice 191 deliberately does NOT ship:

- **Refresh-token grant** (RFC 6749 §6). Re-acquire tokens via
  the device-code flow or `client_credentials` on expiry.
- **DPoP** (RFC 9449). Bearer tokens only in v1.
- **mTLS client authentication** (RFC 8705). `client_secret_post`
  only.
- **credstore package removal**. The legacy package stays in the
  tree (P0-191-2) so existing api_keys rows remain queryable for
  forensic purposes; only the middleware mount retires.

## Troubleshooting

### 410 Gone with no `migration_url`

If your deployment's `ATLAS_OAUTH_DEPRECATION_URL` is unset, the
410 body omits the field. Set the env var to an absolute URL
pointing at this doc (or your own migration runbook) and restart
the atlas process.

### `atlas login` says "device code expired"

The 15-minute approval window passed before you clicked Approve.
Re-run `atlas login` and approve faster — or extend the window by
overriding `--timeout`.

### "slow_down" error during `atlas login`

The CLI is polling faster than the RFC 8628 §3.5 5-second floor.
The CLI auto-extends its poll interval on `slow_down`; subsequent
polls should succeed. If it persists, your local clock may be
skewed — `ntpdate` / `chronyd` fixes that.

### OAuth client_secret leaked

Revoke immediately via the admin UI (`/admin/oauth-clients` —
slice 192 work-in-progress) or directly by setting `disabled_at`
on the `oauth_clients` row. Then re-issue with
`atlas-cli oauth issue-client`. The leaked secret cannot be used
to mint a new token after the row is disabled (slice 188's
`oauthclient.Verify` returns `invalid_client`).
