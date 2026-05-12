# Okta connector

Emits three evidence kinds:

| Kind                     | Profile | Source                                                 |
| ------------------------ | ------- | ------------------------------------------------------ |
| `okta.mfa_policy.v1`     | pull    | `GET /api/v1/policies?type=MFA_ENROLL`                 |
| `okta.app_assignment.v1` | pull    | `GET /api/v1/apps` + `GET /api/v1/apps/{id}/groups`    |
| `okta.user_lifecycle.v1` | pull    | `GET /api/v1/users` + `GET /api/v1/users/{id}/factors` |

## Auth — least-privilege Okta admin scopes

Authentication uses an Okta API token issued to a service account with
the built-in **Read-only Administrator** role. The token must grant
exactly the read-only resource permissions listed below and no more. The
`atlas-okta scopes` subcommand prints this list at runtime.

| Token kind                       | Name                 | Access | Gates                                                       |
| -------------------------------- | -------------------- | ------ | ----------------------------------------------------------- |
| API token (Read-only Admin role) | `okta.users.read`    | Read   | `okta.user_lifecycle.v1` (GET /api/v1/users + factors)      |
| API token (Read-only Admin role) | `okta.groups.read`   | Read   | `okta.app_assignment.v1` (GET /api/v1/apps/{id}/groups)     |
| API token (Read-only Admin role) | `okta.apps.read`     | Read   | `okta.app_assignment.v1` (GET /api/v1/apps)                 |
| API token (Read-only Admin role) | `okta.policies.read` | Read   | `okta.mfa_policy.v1` (GET /api/v1/policies?type=MFA_ENROLL) |

**Banned admin roles:** Super Administrator, Organization Administrator,
Application Administrator (write tier), Group Administrator (write tier),
Help Desk Administrator. The connector's `DocumentedScopes` registry
rejects any future widening (write / delete / admin keywords in the
Access field) at the unit-test level.

The API token is supplied **only** via the `OKTA_API_TOKEN` environment
variable. The `--token` CLI surface is intentionally not advertised: the
env-var path is the documented preferred path so the token never
appears in shell history or process listings.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-okta register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Pull MFA policies, app assignments, user lifecycle, and push evidence.
OKTA_API_TOKEN="<api-token>" \
atlas-okta run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --org example \
  --environment prod \
  --okta-base-url https://example.okta.com

# Print the canonical scope list.
atlas-okta scopes
```

## Rate limiting

Okta enforces a default ceiling of 600 requests per minute per API
token (per the Okta API rate-limit documentation). Slice 045 keeps the
pull path conservative — three list calls per run, plus per-resource
lookups for app groups and user factors. A 200-user / 50-app tenant
generates roughly 253 requests per full run, well under the budget.
Pagination is deferred to a follow-up slice when org sizes demand it.

## Anti-criteria (P0)

- Requires admin API token → REJECTED. The documented scopes are the
  Read-only Administrator's permissions; the `DocumentedScopes` test
  fails when any Access field gains "write" / "delete" / "admin".
- Logs the API token → REJECTED. `oktaauth.Credential.String()` redacts
  the bearer; `%v` / `%+v` formatting paths are pinned by unit test.
- Pushes without `idempotency_key` → REJECTED. Every emitter routes
  through `internal/idem`, which derives a SHA-256 key per
  `(<kind>|<resource_id>|<hour>)`.
- Documents the API token in CLI help text → REJECTED. The `--token`
  flag is hidden; the help text directs operators to the
  `OKTA_API_TOKEN` env var.

## Tests

```sh
go test ./connectors/okta/...
```

Unit tests use `httptest.NewServer` to replay realistic Okta REST
payloads. The integration test exercises the full path:
`oktapolicy.Pull` / `oktaapps.Pull` / `oktausers.Pull` → emitter
builder → SDK Push → in-process bufconn platform → Push receipt.

The dedup test confirms two pushes with identical observed-hour keys
collapse to the same `record_id` for each of the three kinds.
