# Okta connector

Emits three evidence kinds:

| Kind                     | Profile | Source                                                                                  |
| ------------------------ | ------- | --------------------------------------------------------------------------------------- |
| `okta.mfa_policy.v2`     | pull    | `GET /api/v1/policies?type=MFA_ENROLL` + `GET /api/v1/groups` (Everyone)                |
| `okta.app_assignment.v1` | pull    | `GET /api/v1/apps` + `GET /api/v1/apps/{id}/groups` + `GET /api/v1/policies/{id}/rules` |
| `okta.user_lifecycle.v1` | pull    | `GET /api/v1/users` + `GET /api/v1/users/{id}/factors`                                  |

`okta.mfa_policy.v2` (#2901) replaces v1, which the connector no longer
emits. One record per MFA enrollment policy, of any status. It reads both
settings shapes Okta returns: Classic `settings.factors` and Identity
Engine `settings.authenticators[]`. Every FIDO key (`fido_webauthn`,
`fido_u2f`, `webauthn`) is reported as `webauthn`, and the password is
left out of `factors_allowed` and `mfa_required`. `applies_to_groups`
carries Okta group ids; `applies_to_everyone` says whether one of them is
the org's Everyone group, found as the `BUILT_IN` group named `Everyone`.
Okta hides that group from a Group Administrator token, and the pull stops
with an error rather than guess.

`okta.app_assignment.v1` is pushed at schema 1.1.0 (#2828), one record per
app. Two fields decide `soc2_cc6_6_mfa_external_threats`:

- `classification` is `admin`, `infrastructure` or `standard`, from the
  app ids in `--admin-apps` and `--infrastructure-apps` (comma-separated or
  repeated); once either list is set, every other app is `standard`. With
  neither set, no app carries a classification. Okta has no app tag the
  connector could read instead. A listed id that matches no app is
  reported on stderr.
- `sign_on_mfa_required` comes from the app sign-in policy that the app's
  `_links.accessPolicy` names (Identity Engine). It is true when every
  active rule denies access or allows it with an `ASSURANCE` method whose
  `factorMode` is `2FA`, and false when an active rule allows `1FA`,
  whatever that rule's conditions. It is omitted, with `reason`, when the
  app names no policy (Classic Engine orgs), when a rule uses another
  method (`AUTH_METHOD_CHAIN`, `ID_PROOFING`), or when the rules could not
  be read. Each policy's rules are read once per run. MFA required only by
  the global session policy is not counted.

## Auth — least-privilege Okta admin scopes

Authentication uses an Okta API token issued to a service account with
the built-in **Read-only Administrator** role. The token must grant
exactly the read-only resource permissions listed below and no more. The
`atlas-okta scopes` subcommand prints this list at runtime.

| Token kind                       | Name                 | Access | Gates                                                                                                                                           |
| -------------------------------- | -------------------- | ------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| API token (Read-only Admin role) | `okta.users.read`    | Read   | `okta.user_lifecycle.v1` (GET /api/v1/users + factors)                                                                                          |
| API token (Read-only Admin role) | `okta.groups.read`   | Read   | `okta.app_assignment.v1` (GET /api/v1/apps/{id}/groups) + `okta.mfa_policy.v2` (Everyone id)                                                    |
| API token (Read-only Admin role) | `okta.apps.read`     | Read   | `okta.app_assignment.v1` (GET /api/v1/apps)                                                                                                     |
| API token (Read-only Admin role) | `okta.policies.read` | Read   | `okta.mfa_policy.v2` (GET /api/v1/policies?type=MFA_ENROLL) + `okta.app_assignment.v1` `sign_on_mfa_required` (GET /api/v1/policies/{id}/rules) |

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
  --okta-base-url https://example.okta.com \
  --admin-apps 0oa1admin,0oa2aws

# Print the canonical scope list.
atlas-okta scopes
```

## Rate limiting

Okta enforces a default ceiling of 600 requests per minute per API
token (per the Okta API rate-limit documentation). Slice 045 keeps the
pull path conservative — three list calls per run, plus per-resource
lookups for app groups and user factors, and one rules call per distinct
app sign-in policy. A 200-user / 50-app tenant
generates roughly 253 requests per full run, well under the budget.
Every list call follows Okta's `Link: <…>; rel="next"` cursor until the
listing is exhausted (#2915), so each further page of 200 adds one
request. A next link that leaves the configured org's scheme and host is
refused, so the token is never sent elsewhere, and one listing stops
with an error after 1,000 pages.

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
