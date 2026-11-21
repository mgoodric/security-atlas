# #2823 — cc8_1 change management and configuration change control as SQL payload checks (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
Implements ADR 0028 tier 1, checks 4 and 5, following the cc6_1 tracer's
pattern (`docs/audit-log/2821-cc6-1-sql-payload-check-decisions.md` D1)
and cc6_6's shape (`docs/audit-log/2822-cc6-6-host-firewall-sql-check-decisions.md`).

- detection_tier_actual: none
- detection_tier_target: unit

No bug surfaced while building. The connector gaps in D5 were found by
reading the connector and GitHub's REST docs, not by a test.

## D1 — Each repository's latest record decides

The connector pushes one `github.repo_protection.v1` record per repository
per run (`idem.RepoProtectionKey(repo, hour)`), so a daily window can hold
several for one repository. Each query takes the latest per
`repo_full_name` (`DISTINCT ON ... ORDER BY observed_at DESC`) and requires
every one to qualify. Bundle tests pin both directions: on after off
passes, off after on fails.

## D2 — Ties at one `observed_at` break toward fail

The connector truncates `observed_at` to the hour, so two records for one
repository can share an instant (a re-run under another schema version, or
a second pusher). Each row's verdict is computed first as `ok`, and the
`DISTINCT ON` order is `repo_full_name, observed_at DESC, ok`. `false`
sorts before `true`, so a disagreeing tie picks the failing record. The
result is deterministic and fail-closed. Bundle case
`tie-at-one-instant-fails-closed` pins it; flipping its expectation fails
`TestStockSQLBundleTests_PassAgainstPostgres`.

## D3 — Missing, mistyped or keyless fields fail

- `required_reviews` must be a JSON number of at least 1. A `CASE` on
  `jsonb_typeof` guards the `::numeric` cast, so a string value fails
  rather than erroring the evaluation.
- `enforce_admins` (change management only) must be JSON `true`; a
  missing field fails via `coalesce(..., false)`.
- A record with no or empty `repo_full_name` fails. All keyless records
  collapse into one `DISTINCT ON` group, and that group's `ok` is false.
  The schema requires the field, so this is defence for a record that
  bypassed validation.
- Every term is coalesced, so `ok` is never NULL and `bool_and` can't skip
  a row. The outer `coalesce(bool_and(ok), false)` returns false on an
  empty set, and the engine reports an empty window as inconclusive
  (#2821 D1).

## D4 — Configuration change control does not check admin enforcement

ADR 0028 check 5 is "at least one approving review", with the `iac`-topic
scoping in #2827. It is not given the `enforce_admins` term, so the two
bundles stay distinct checks. Its pass case has `enforce_admins` false to
pin that.

## D5 — Connector findings (reported to the dispatcher, not worked around)

Read from `connectors/github/internal/githubrepo/githubrepo.go`,
`connectors/github/cmd/atlas-github/cmd_run.go` and
`internal/evidence/ingest/ingest.go`, and checked against GitHub's REST
docs for "Get branch protection", "Get rules for a branch" and "List
organization repositories" (apiVersion 2022-11-28).

1. **Field shapes match.** The docs give `enforce_admins` as
   `{url, enabled}` and `required_pull_request_reviews` with
   `required_approving_review_count` (integer, 0 to 6) and
   `require_code_owner_reviews`. `BranchProtection` decodes exactly those.
2. **An unread repository reads as unprotected.** On any error other than
   404, `Inspect` pushes the repository with `Result=inconclusive` and the
   zero value of every field, so the payload says `required_reviews: 0`,
   `enforce_admins: false`. The queries read the payload, so such a
   repository fails both controls. That is fail-closed, and both
   descriptions say so. The fix belongs in the connector: omit fields it
   did not read (the GitHub counterpart of #2912).
3. **404 is treated as "no protection".** GitHub documents only
   "404 - Resource not found" for this endpoint. The connector reads it as
   unprotected and pushes `fail`. **Unverified:** whether a token lacking
   the needed permission also gets 404 here. If it does, a mis-scoped token
   fails every repository rather than going inconclusive.
4. **Rulesets are not read.** The connector calls only the classic
   branch-protection endpoint. GitHub's "Get rules for a branch"
   (`GET /repos/{owner}/{repo}/rules/branches/{branch}`) returns active
   rules "regardless of the level at which they are configured", including
   org rulesets. A repository protected only by a ruleset gets 404 from the
   classic endpoint, so it is pushed as unprotected and fails both
   controls. **Unverified** against a live org.
5. **Only the first 100 repositories are read.** `ListOrgRepos` requests
   `per_page=100` with no paging (its comment says so). Repositories past
   the first 100 are never pushed, so the controls don't see them.
6. **The records may not reach these controls.** Ingest links a record
   only when its `control_id` is a UUID or an active bundle id
   (`ingest.go`, #2100). The connector's `--repo-protection-control`
   defaults to `scf:TDA-06`, which is neither, and a run sets one control
   id for every record. So with defaults neither cc8_1 bundle receives
   these records, and one run can feed at most one of the two. Read from
   the code; not exercised end to end here. The same shape applies to
   osquery's `--host-posture-control` (`scf:END-03`) for cc6_6 and cc4_1.

None of these changes what the queries should say, so this PR doesn't
compensate for them.

## D6 — Supersedes the stock bundles `soc2_cc8_1_change_management` and `soc2_cc8_1_configuration_change_control`

Each query's `language`, `expression` and `id` change, so both bundles
supersede on the next boot (#1908). Owners, evidence, evaluations,
exceptions and links stay on the old rows (#2820 D1). Since they
supersede anyway, the stale strings go too:

| Bundle                          | Old                                                           | New                                                            |
| ------------------------------- | ------------------------------------------------------------- | -------------------------------------------------------------- |
| change management, query id     | `prod_repos_have_branch_protection`                           | `every_repo_requires_review_enforced_on_admins`                |
| change management, title        | "Production-deploying repositories enforce branch protection" | "Repositories require an approving review, enforced on admins" |
| config change control, query id | `iac_repos_require_reviewed_prs`                              | `every_repo_requires_approving_review`                         |
| config change control, title    | "Infrastructure changes go through reviewed pull requests"    | "Repositories require an approving review before merge"        |

The old titles and query descriptions claimed production and `iac`
scoping that nothing reads; that waits on #2827. The changelog fragment
carries the upgrade note.
