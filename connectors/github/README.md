# GitHub connector

Emits three evidence kinds:

| Kind                        | Profile | Source                                                               |
| --------------------------- | ------- | -------------------------------------------------------------------- |
| `github.repo_protection.v2` | pull    | `GET /orgs/{org}/repos` + branch protection API + rules for a branch |
| `github.scim_user.v1`       | pull    | `GET /scim/v2/organizations/{org}/Users`                             |
| `github.audit_event.v1`     | push    | Organization webhook deliveries                                      |

## Auth — least-privilege PAT scopes

Slice 044 supports two auth modes:

- **Personal Access Token (recommended for v1).** Fine-grained PAT preferred over classic.
- **GitHub App.** The configuration surface is wired in this slice; the JWT signer + installation-token exchange land in slice 045. Calling `--use-app` today returns `ErrAppNotWired` — use a PAT.

The PAT must grant exactly these read-only scopes — and no more. The `atlas-github scopes` subcommand prints this list at runtime.

| Token kind       | Permission                 | Access | Gates                                                                                         |
| ---------------- | -------------------------- | ------ | --------------------------------------------------------------------------------------------- |
| Fine-grained PAT | Repository: Administration | Read   | `github.repo_protection.v2` (branch protection rules and rulesets)                            |
| Fine-grained PAT | Repository: Metadata       | Read   | Listing repos under the org                                                                   |
| Fine-grained PAT | Organization: Members      | Read   | `github.scim_user.v1` (falls back to org membership when SCIM is unavailable)                 |
| Fine-grained PAT | Organization: Webhooks     | Read   | `webhook` subcommand: verifying that an org webhook is configured for `github.audit_event.v1` |

**Banned scopes:** `admin:org`, `delete_repo`, `repo` (full write), any `write:*`. The connector's `DocumentedScopes` registry rejects these at the test level.

## `github.repo_protection.v2`

One record per non-archived repository in the org, for its default
branch. Before v2 (`github.repo_protection.v1`, still registered), an
unreadable repository was pushed with every field `0` or `false`,
rulesets were ignored, only the first 100 repositories were read, and
records carried no stable id (#2925).

- **Paging.** `GET /orgs/{org}/repos` and `GET /repos/{o}/{r}/rules/branches/{b}`
  follow the `Link: rel="next"` header. A next link that leaves
  `--github-base-url`'s scheme and host is refused, so the token is never
  sent elsewhere.
- **Classic protection and rulesets are merged.** The connector reads
  `GET /repos/{o}/{r}/branches/{b}/protection` (404 means no classic
  protection) and `GET /repos/{o}/{r}/rules/branches/{b}`, which returns
  every active rule from repository and organization rulesets. GitHub
  enforces each source on its own, so the strictest applies:
  `required_reviews` is the highest required approving review count, and
  a setting any source requires is on. `protection_source` says which
  sources protect the branch: `none`, `classic`, `rulesets` or
  `classic_and_rulesets`.
- **Admin enforcement.** `enforce_admins` is true when a source that
  requires an approving review binds admins: classic `enforce_admins`, or a
  ruleset whose bypass actors (`GET /repos/{o}/{r}/rulesets/{id}`) are
  only apps (`Integration`) or deploy keys. A role, team or user bypass
  actor may be an admin, so it counts as admin bypass. GitHub returns
  `bypass_actors` only to a caller with write access to the ruleset. When
  the answer rests on a ruleset whose bypass actors are withheld,
  `enforce_admins` is omitted and `reason` says so.
- **Unread fields are omitted.** If either protection request fails with
  anything other than the classic endpoint's 404, the record carries only
  `repo_id`, `repo_full_name`, `default_branch` and `reason`, with result
  `inconclusive`. It is never pushed as unprotected.
- **Stable id.** `repo_id` is GitHub's repository id, which survives a
  rename. The idempotency key and the cc8_1 checks key on it.

**Unverified:** which fine-grained permission GitHub requires for the
rules and ruleset endpoints. The scopes table above has not been checked
against a live org for them. A request the token can't make fails, and
the repository goes inconclusive rather than failing.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-github register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Pull org repos + SCIM users and push evidence.
atlas-github run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --org example \
  --environment prod

# Start the webhook receiver for github.audit_event.v1.
GITHUB_WEBHOOK_SECRET=... \
atlas-github webhook \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --addr :8080 \
  --path /webhook \
  --environment prod
```

## Webhook security

Every delivery posted to `--path` is:

1. **HMAC-SHA256 verified** against `$GITHUB_WEBHOOK_SECRET` using `crypto/subtle.ConstantTimeCompare` via `hmac.Equal`. Unsigned, missing, malformed, or wrong-key signatures return HTTP 401 with no record pushed.
2. **Identified** by the verbatim `X-GitHub-Delivery` UUID, which becomes the evidence record's `idempotency_key`. Re-deliveries by GitHub do not double-write the ledger.
3. **Scoped** to `(org, environment)`. The `org` field is derived from the webhook body's `organization.login`. Deliveries without an `organization.login` are rejected (slice 044 is org-scoped only).

The webhook secret is read **only** from `$GITHUB_WEBHOOK_SECRET`. The connector refuses to start if it is empty. No flag accepts the secret, so it never lands in shell history or process listings.

## Anti-criteria (P0)

- Requires admin PAT → REJECTED. Documented scopes are read-only.
- Skips webhook signature verification → REJECTED. The handler returns 401 before any push.
- Logs GitHub secrets / PATs → REJECTED. `Credential.String()` redacts; the README is the only place tokens are documented.
- Pushes without `idempotency_key` derived from event id → REJECTED. The handler returns 400 when `X-GitHub-Delivery` is missing.

## Tests

```sh
go test ./connectors/github/...
```

Unit tests use `httptest.NewServer` to replay realistic GitHub REST + SCIM payloads. The HMAC fixture pins both the accept and reject paths (correct signature, missing header, wrong prefix, bad hex, tampered body, wrong secret).
