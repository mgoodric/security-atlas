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

| Token kind       | Permission                 | Access | Gates                                                                                                                 |
| ---------------- | -------------------------- | ------ | --------------------------------------------------------------------------------------------------------------------- |
| Fine-grained PAT | Repository: Administration | Read   | `github.repo_protection.v2`: classic protection, `GET /repos/{o}/{r}/branches/{b}/protection`                         |
| Fine-grained PAT | Repository: Metadata       | Read   | Listing repos under the org; rulesets, `GET /repos/{o}/{r}/rules/branches/{b}` and `GET /repos/{o}/{r}/rulesets/{id}` |
| Fine-grained PAT | Repository: Contents       | Read   | Optional. `GET /repos/{o}/{r}/branches/{b}`, cross-checking a classic protection 404 (below)                          |
| Fine-grained PAT | Organization: Members      | Read   | `github.scim_user.v1` (falls back to org membership when SCIM is unavailable)                                         |
| Fine-grained PAT | Organization: Webhooks     | Read   | `webhook` subcommand: verifying that an org webhook is configured for `github.audit_event.v1`                         |

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
  `GET /repos/{o}/{r}/branches/{b}/protection` and
  `GET /repos/{o}/{r}/rules/branches/{b}`, which returns
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
  `bypass_actors` only to a caller with write access to the ruleset, and
  doesn't say whether anyone else gets no field or `[]`. So a non-empty
  list is read; an empty list counts as "nobody bypasses" only on a
  repository ruleset (`source_type: Repository`) of a repository the
  listing reports the caller as admin of (`permissions.admin`). An empty
  list on an organization or enterprise ruleset, or for a non-admin
  caller, is treated as withheld. `current_user_can_bypass` is not used:
  GitHub returns it to any caller of the repository endpoint, so it says
  nothing about write access (#2933). When the answer rests on a ruleset
  whose bypass actors are withheld, `enforce_admins` is omitted and
  `reason` says so.
- **A classic protection 404 is not always "unprotected".** GitHub
  returns 404 both for a branch without classic protection and for a
  token that can't read it. Only a 404 whose message is
  `Branch not protected` reads as no classic protection. Any other 404 is
  cross-checked with `GET /repos/{o}/{r}/branches/{b}`: `protected: false`
  reads as no classic protection, and anything else (true, missing, or a
  failed request) leaves the repository inconclusive (#2933).
- **Unread fields are omitted.** If either protection request fails with
  anything other than a classic 404 read as above, the record carries only
  `repo_id`, `repo_full_name`, `default_branch` and `reason`, with result
  `inconclusive`. It is never pushed as unprotected.
- **Stable id.** `repo_id` is GitHub's repository id, which survives a
  rename. The idempotency key and the cc8_1 checks key on it.

**Sources and what is unverified.** The permissions in the scopes table
come from GitHub's [Permissions required for fine-grained personal access
tokens](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens)
page (read 2026-11-21). None of the following has been checked against a
live org:

- the `Branch not protected` message: GitHub's REST description documents
  the 404 only as "Resource not found" and gives no body;
- whether `protected` on `GET .../branches/{b}` is also true for a branch
  protected only by a ruleset. If it is, such a branch whose classic 404
  lacks that message reads inconclusive, never unprotected;
- whether a fine-grained PAT's `permissions.admin` in the repo listing
  reflects the user's role, so an admin's PAT gets the full bypass list.

A request the token can't make fails, and the repository goes
inconclusive rather than failing.

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
