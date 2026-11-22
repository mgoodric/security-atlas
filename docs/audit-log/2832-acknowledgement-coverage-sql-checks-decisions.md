# #2832 — cc1_5, cc5_3 and cc7_4 check acknowledgement coverage in SQL (decisions log)

Parent: #1860 (ADR 0028, tier 1 rows 11-13). Was blocked by #2817 (PR #2869),
which made acknowledgement evidence reach these controls, and is downstream
of #2873, which made the acknowledgement handler the kind's only writer. No
migration, no new schema version.

- detection_tier_actual: manual_review
- detection_tier_target: unit

The bug found: ADR 0028 lists these three as tier 1 ("fields already
emitted"), but the handler emitted only `policy_id`, `policy_version_id`,
`user_id` and `acknowledged_at`. `policy_id` equals `policy_version_id`
(each published version is its own `policies` row), so no query could tell
which policy a record is for, group a policy's versions, or find the IR
plan. Found reading the handler before writing the queries. A test that a
stock SQL check's payload fields are ones the producer emits would have
caught it at the ADR.

## D1 — The handler snapshots `policy_title` and `policy_version` (dispatcher, for Matt, 2026-11-21)

- **Options considered:** (a) leave the payload as it is and check only
  that the UUIDs are well formed; (b) emit the policy's title and version
  string, which schema `policy.acknowledgment/1.0.0` already declares as
  optional snapshots; (c) a new schema version with a stable stock-policy
  key.
- **Chosen:** (b), in this PR.
- **Why:** (a) establishes none of the three claims. (b) is a change in the
  layer that owns the record, needs no schema version (ADR 0028 decision 8
  applies to adding fields; these are declared already), and gives every
  record what the user attested to. (c) needs a stable key the policies
  table doesn't have: a stock policy is identified only by its title
  (`internal/policy/seed`), so (c) would be a schema and a policies
  migration for a key nothing else uses.
- `AckStore.Record` already loads the policy row for its published and
  roster checks; it now returns that row's `Title` and `Version` on `Ack`,
  including on the dedup paths (the token covers the same row).
  `required_roles` is not emitted: no check reads it.
- Records written before this change carry no title. Each query treats
  them as unread or leaves them out (below), so a tenant's checks go
  inconclusive, not fail, until its users acknowledge again or the old
  records leave the window.

## D2 — The rename limit: titles are matched exactly

The five stock titles are matched exactly and case-sensitively, as
`policies/stock/*.md` declares them. A tenant that renames a stock policy
(say "Incident Response Plan (2026)") stops matching: cc1_5 then fails its
users for the missing title, and cc7_4 does not see the plan. Nothing in
the tenant gives a stock policy a stable key that survives a rename, so the
title is the only handle. Each description states the limit. A stable
stock key would be (c) in D1, and is not filed: no ticket asks for renamed
stock policies to keep counting.

## D3 — cc1_5: every user who appears is judged (dispatcher, for Matt)

- The first proposal passed when any one user covered all five; the
  dispatcher's call was to judge every user in the window. Per user, keyed
  by `user_id`, latest record per (user, title) by `observed_at`:

  | User's records                                                    | User   |
  | ----------------------------------------------------------------- | ------ |
  | latest records cover all five titles, each with `result` pass     | pass   |
  | otherwise, any record untitled or more than 1 h after the instant | unread |
  | otherwise (titled, but one or more of the five missing)           | fail   |

  Across users: any fail fails, all pass passes, else inconclusive
  (`least`-style over 0/1/2). No record is inconclusive.

- A user with only pre-#2832 records is unread, not failing: their
  untitled records may be the missing policies.
- Tie on `observed_at`: the title counts only if every tied record passes
  (worse wins, `tied-latest-with-a-failing-record-fails`). The handler
  writes only pass, so this is defensive.
- Records without `user_id` are left out. The handler always sets it.
- Users who have acknowledged nothing are not in the evidence and are not
  counted. The description says so; "every active user" waits on #2834.

## D4 — cc5_3: version in force, as far as received records show

- The handler refuses an acknowledgement of a version that is not
  published (409), and since #2873 nobody else can write the kind. So a
  record names the version in force when it was recorded, by
  construction. What a query can still check across records is ordering.
- Keyed by (`policy_title`, `policy_version_id`); records without either
  are left out. Within a title, each version's first non-future
  acknowledgement in the window orders the versions. A record fails when a
  different version of its title was first acknowledged after its own
  version's first acknowledgement and before this record: it acknowledges
  a version the title had already moved past (`superseded-version-acknowledged-fails`).
  Equal instants are not superseded (strict comparisons).
- Unread: no `policy_version`, `result` not pass, or `observed_at` more
  than 1 h after the instant. A future record also takes no part in the
  version order (`future-newer-version-does-not-supersede`).
- Version order is by first acknowledgement, not by parsing the semver
  string. Semver is free text on policies; first-acknowledgement order is
  what the records show, and the handler makes it agree with publish order
  for any two versions that were both acknowledged: a version can only be
  acknowledged while published, before its successor is published.
- **Known false finding:** two published policies with the same title
  (titles are not unique) are read as versions of one policy, and
  interleaved acknowledgements of them fail the control. Stated in the
  description. The stock titles are seeded once per tenant, so this needs
  a tenant-authored duplicate.
- Review cadence ("each policy within its review cadence") is not checked;
  the description says so.

## D5 — cc7_4 never fails

- Passes when any in-window record with `result` pass is of the title
  "Incident Response Plan" and is not more than 1 h after the instant;
  otherwise inconclusive.
- Received records can show that someone acknowledged the plan, never that
  an on-call person did not, so no state is fail until the on-call
  inventory (#2834). The dispatcher accepted this when settling D1.
- The ticket's acceptance asks for pass and fail cases for each check.
  cc7_4 has none for fail, and `TestStockSQLChecksShipBundleTests` requires
  one of every SQL check. The guard gains `stockSQLChecksWithoutFail`, a
  named exemption with a reason, which itself fails if the listed bundle
  ever ships a fail case. cc7_4's evaluation test covers pass,
  inconclusive (other policies only) and the empty ledger.

## D6 — Shape follows cc6_7 / cc4_1

- Time is `evaluated_at` only, never `now()`. No payload timestamp is
  read: `observed_at` is `acknowledged_at` (the handler sets both from the
  same value), so no RFC 3339 guard is needed. The +1 h future cap applies
  to `observed_at`, as in cc6_5 / cc9_1 / cc6_7.
- No window of the checks' own: the freshness windows (weekly 30 d for
  cc1_5 and cc5_3, quarterly 120 d for cc7_4) bound the records.
- The SQL sandbox refuses dotted references (`r.title` reads as a
  schema-qualified name), so cc5_3's correlated subquery uses distinct
  column names (`other_title`, ...) instead of aliases.

## D7 — Supersedes all three stock bundles

The query language and expression change, so each bundle supersedes on the
next boot (#1908). Query ids stay; each query's `description` is corrected
(#2820 D1). Owners, evidence, evaluations, exceptions and risk links stay
on the old rows and have to be moved by hand. The PR carries the
`supersedes-stock` label; the changelog fragment carries the upgrade note.

**Upgrade note:** on the first start after upgrading,
`soc2_cc1_5_accountability_acknowledgment`, `soc2_cc5_3_policies_procedures`
and `soc2_cc7_4_incident_response` are superseded by new versions.
Re-assign their owners and move exceptions and risk links to the new
versions; the old versions keep their history. A tenant whose cc1_5 passed
on any acknowledgement can now fail (a user who has acknowledged some but
not all five) or go inconclusive (acknowledgements recorded before the
upgrade carry no title, until they leave the 30-day window). cc7_4 goes
inconclusive until someone acknowledges the IR plan after the upgrade.

## Tests

- `tests/` cases: cc1_5 19, cc5_3 15, cc7_4 9, run against Postgres by
  `TestStockSQLBundleTests_PassAgainstPostgres`. Against main's Rego
  roll-ups (`atlas-cli controls test` on the old `control.yaml` with these
  cases), 11, 7 and 4 of them fail.
- `TestAckChecks_EngineEvaluatesAcknowledgementPayload` (internal/control):
  the live engine over ledger records; pass and fail for cc1_5 and cc5_3,
  pass and inconclusive for cc7_4, and the empty ledger for each. Against
  main's bundles: `one user misses a title`, `superseded version
acknowledged` and `other policies only` all came out pass.
- `TestAcknowledge2832_StockAckChecksReadTitleAndVersion`
  (internal/api/policyacks): records written through
  `POST /v1/policies/{id}/acknowledge` carry `policy_title` and
  `policy_version`; one user missing four titles fails cc1_5, and passes
  once they acknowledge them. With the handler change reverted, the payload
  assertion failed and all three controls were inconclusive.
- `TestAcknowledge2817_ReachesStockAckControls` keeps asserting the record
  reaches each control (1 in window); its verdicts are now fail / pass /
  inconclusive for one user acknowledging the Information Security Policy.
