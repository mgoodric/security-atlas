# #2925 — GitHub connector: `github.repo_protection.v2`, and cc8_1 reads it (decisions log)

Follows from [#2823](https://github.com/mgoodric/security-atlas/issues/2823)'s
D5 (`docs/audit-log/2823-cc8-1-repo-protection-sql-checks-decisions.md`),
which reported the connector gaps without compensating for them. Same shape
as the Okta (#2901, `okta.mfa_policy.v2`) and osquery (#2912) fixes.

- detection_tier_actual: manual_review
- detection_tier_target: contract

The bugs were found by reading the connector against GitHub's REST docs
(#2823 D5), not by a test. A conformance test that runs the connector's
real record through `schemaregistry.NewEmbedded().ValidatePayload`, with
fixtures shaped from the provider's documented responses, is the tier that
should have caught them. This PR adds one.

GitHub's API shapes were read from GitHub's OpenAPI description
(`github/rest-api-description`,
`descriptions/api.github.com/api.github.com.json`, read 2026-11-21) and
the docs pages for "Get rules for a branch", "Get a repository ruleset",
"List organization repositories", "Get branch protection" and "Using
pagination in the REST API". Nothing here was checked against a live org.

## D1 — A new kind, `github.repo_protection.v2` at 2.0.0, not a minor

The ticket asked for a schema minor. Omitting an unread field means
`required_reviews` can no longer be required. `CheckAdditiveOver` rejects a
minor that drops a required field, and the registry enforces it on every
minor registration (`service.go`). So this is a breaking change, and ADR 0028
decision 8 makes it a new version. Following #2901, it is a new kind at 2.0.0.
The dispatcher confirmed the call. `github.repo_protection.v1`/1.0.0 stays
registered, so existing ledger rows stay valid.

v2 requires `repo_id`, `repo_full_name` and `default_branch`. The
protection fields are optional. It adds `protection_source` (`none`,
`classic`, `rulesets`, `classic_and_rulesets`) and `reason`.

Two ways to stay on a minor were rejected:

- Keeping `required_reviews` required and not pushing unread repos. That
  would make an unread repo invisible to the control, a silent gap instead
  of an inconclusive record.
- Making `required_reviews` nullable. That is a type change, which the
  additive check also rejects.

## D2 — An unread repo carries no protection fields

If classic protection returns anything other than 200 or 404, or the
rules endpoint returns anything other than 200, the record carries only
`repo_id`, `repo_full_name`, `default_branch` and `reason`. Its result is
`inconclusive`. A `pull_request` rule whose parameters don't decode is
treated the same way. A partial read is not pushed as a lower bound,
because a ruleset the connector failed to read could tighten anything.

The classic endpoint's 404 still means "no classic protection". It is
the only documented non-200 response. **Still unverified** (#2823 D5.3):
whether a token that lacks the permission also gets a 404 there. If it
does, a mis-scoped token reads as "no classic protection". The rules
request still runs, and an unreadable rules endpoint makes the repo
inconclusive.

## D3 — Classic protection and rulesets merge to the strictest setting

GitHub enforces each source on its own. "Get rules for a branch" returns
the active rules from every level. Rules in `evaluate` or `disabled`
rulesets are not returned. The merge:

- `required_reviews` is the highest of classic
  `required_approving_review_count` and every `pull_request` rule's
  `required_approving_review_count`.
- `require_code_owner_reviews` is set when classic
  `require_code_owner_reviews` or any rule's `require_code_owner_review` is
  set. The ruleset field name is singular.
- `require_signed_commits` and `require_linear_history` are set by the
  classic setting or by a `required_signatures` or
  `required_linear_history` rule.

## D4 — When a ruleset's review binds admins

Classic protection has a direct flag, `enforce_admins.enabled`. Rulesets
have bypass actors instead. "Get rules for a branch" does not return them,
so the connector reads `GET /repos/{o}/{r}/rulesets/{ruleset_id}` for each
ruleset whose `pull_request` rule requires at least one review. The
`ruleset_id` comes from the rule's `repository-rule-ruleset-info` fields,
and `includes_parents` defaults to true, so org rulesets resolve too.

A ruleset binds admins when none of its bypass actors may be an admin. Of
GitHub's actor types, only `Integration` (an app) and `DeployKey` are never
a person. `OrganizationAdmin`, `RepositoryRole`, `Team` and `User` may be or
include an admin. The connector doesn't resolve role ids or team
membership, so any of these counts as admin bypass, whatever its
`bypass_mode`. That is the fail-closed reading. A `Team` bypass with no
admin in the team will read as `enforce_admins: false`.

`enforce_admins` is:

- **true** when a source that requires an approving review binds admins.
- **false** when every such source was read and none binds.
- **omitted** when the answer rests on a ruleset whose bypass actors could
  not be read. GitHub returns `bypass_actors` only to a caller with write
  access to the ruleset, so a read-only token usually gets this.
- with no source requiring a review, classic `enforce_admins`, or false if
  there is no classic protection. The control fails on `required_reviews`
  in that case anyway.

Rulesets are read only until one binds, so a branch whose classic
protection already binds admins costs no ruleset request.

## D5 — Paging follows `Link: rel="next"`, on the API host only

`ListOrgRepos` and `GetBranchRules` follow the `Link` header's `rel="next"`
target until there is none. Both endpoints document `per_page` with a
maximum of 100. The next URL must keep `--github-base-url`'s scheme and
host, otherwise the run errors: the credential goes on every page request,
and a link to another host would carry it there. A 1,000-page cap
(100,000 items at `per_page=100`) stops a server that never ends the
chain.

## D6 — Records and checks key on `repo_id`

`repo_id` is GitHub's repository `id` (integer, int64), which survives a
rename. The idempotency key moves to
`sha256("github.repo_protection.v2|<repo_id>|<hour>")`. A rename within the
hour no longer mints a second record, and no v2 key collides with a v1 key
from the same hour.

Both cc8_1 queries group by `payload->'repo_id'`. A renamed repository's
records stay one group, and its latest record decides. Bundle case
`rename-keeps-one-repo` pins this.

## D7 — The queries' three verdicts, and the tie order

Each record gets a verdict rank: 0 for fail, 1 for inconclusive, 2 for pass.

- **Fail (0):** `repo_id` missing, not a number, or below 1. Also
  `required_reviews` present but not a number, or below 1, and for change
  management, `enforce_admins` present and not `true`.
- **Inconclusive (1):** `required_reviews` absent, or for change
  management, `enforce_admins` absent.
- **Pass (2):** anything else.

Per repo, the latest record decides. The `DISTINCT ON` order is
`repo_id, observed_at DESC, rank`, so a tie at one instant takes the lowest
rank and breaks toward fail (#2823 D2). The control is `fail` if any repo
fails, otherwise `inconclusive` if any repo is unread, otherwise `pass`. No
record is `inconclusive`.

This changes #2823 D3 on purpose. There, a missing field failed, because
v1 always carried every field and a missing one could only be malformed.
In v2 a missing field means "not read", and reporting that as a fail is
the zeros bug again in another form. A known failure still outranks an
unread repo, so a read repo that is off fails the control regardless.

The query declares `evidence_kind: github.repo_protection.v2`, so the
engine filters v1 records out (ADR 0028 decision 5). A window holding only
v1 records is therefore inconclusive, never a fail. Bundle case
`v1-record-is-not-read` covers that.

## D8 — Supersedes the stock bundles `soc2_cc8_1_change_management` and `soc2_cc8_1_configuration_change_control`

Each query's `evidence_kind` and `expression` change, so both bundles
supersede on the next boot (#1908). The query ids and titles do not
change: they still describe what is checked. Owners, evidence,
evaluations, exceptions and links stay on the old rows (#2820 D1). This PR
carries the `supersedes-stock` label (#2891). The changelog fragment
carries the upgrade note.

## D9 — Not changed here

- The `--repo-protection-control` flag and ingest routing are untouched.
  #2924 owns how these records reach cc8_1 (#2823 D5.6).
- Which fine-grained permission the rules and ruleset endpoints need is
  **unverified**. The README says so. A request the token can't make
  leaves the repo inconclusive, or leaves `enforce_admins` unread. It
  never fails the repo.
- ADR 0028's check table still names `github.repo_protection.v1` for
  checks 4 and 5. Like #2901, this PR doesn't edit the ratified ADR.
