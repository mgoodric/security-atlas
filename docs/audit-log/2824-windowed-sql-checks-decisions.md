# #2824 — windowed SQL checks: cc6_3, cc1_4, cc6_5, cc9_1 (decisions log)

[#2824](https://github.com/mgoodric/security-atlas/issues/2824), checks 6–9
of ADR 0028's tier 1. Four stock checks whose intent is a time window move
from a roll-up of the record's own verdict to SQL over `observed_at` or a
payload timestamp, measured against the sandbox's `evaluated_at` (#2911).
The pattern follows cc4_1 (#2822, PR #2932): rows without an entity key are
left out, timestamps in the payload are guarded before the cast, a value
more than an hour after the instant is unreadable, and the verdict is
three-valued.

The old intent is at `bf99aaa5`, the parent of `3f0ae1fb` (#1858):

| Bundle                       | Old Rego (never ran)                                                                 | Now                                                             |
| ---------------------------- | ------------------------------------------------------------------------------------ | --------------------------------------------------------------- |
| cc6_3 periodic access review | every in-scope system has a `completed` review with `completed_at` in 90 d           | any keyed review with `observed_at` in 90 d                     |
| cc1_4 training records       | `assignment_id` set, `completed_at` in 365 d, source in {manual, csv, lms_connector} | each person's latest completion in 365 d from an allowed source |
| cc6_5 asset disposal         | an upload for this bundle with `observed_at` in 90 d                                 | the same                                                        |
| cc9_1 business continuity    | an upload for this bundle with `observed_at` in 365 d                                | the same                                                        |

- detection_tier_actual: integration
- detection_tier_target: integration

One bug surfaced and was fixed before push (D2). The Postgres bundle run
caught it; the unit tier can't, because SQL needs a database.

## D1 — What each window is measured on

- **cc6_3:** `observed_at`. `access_review.completion/1.0.0` has no
  completion time or status field. The platform writes the record only
  when a campaign completes with no item pending
  (`internal/accessreview/store.go` `Complete`), with `observed_at` set to
  that instant, so every record is a completed review and `observed_at` is
  its completion time. The old Rego's `r.status == "completed"` and
  `r.completed_at` were fields the schema never had.
- **cc6_5, cc9_1:** `observed_at`, as the old Rego did. `manual.upload/1.0.0`
  carries no source time. `observed_at` is the push time, and the
  description says that isn't always when the file was written (see D6).
- **cc1_4:** the payload's `completed_at`, which the schema requires and the
  old Rego read. The platform writer sets `observed_at` to the same value,
  but a direct pusher need not, and the control asks when the person
  completed training. `completed_at` is a string, so it gets cc4_1's guard
  (anchored RFC 3339 regex plus `pg_input_is_valid`). A value that fails
  the guard is unreadable and never errors the query. Cases
  `malformed-completed-at-*` pin both halves.

The registry doesn't assert `format: date-time`. I checked by compiling the
embedded schema and validating `completed_at: "not a time"`: only the
`completion_source` enum was rejected. So the guard is load-bearing.

## D2 — Windows are counted in hours, not days

The first head used `interval '90 days'` and `interval '365 days'`. The
Postgres bundle run failed `review-just-over-90d-fails` and
`upload-just-over-90d-fails`: a record at `2026-08-23T11:59Z` passed against
`evaluated_at` `2026-11-21T12:00Z`. `timestamptz - interval 'N days'`
subtracts calendar days in the session's `TimeZone`, and the scratch
cluster's zone has a DST change between August and November. The bound moved
by an hour, so the verdict depended on the server's time zone setting.

The queries now use `interval '2160 hours'` (90 × 24) and `'8760 hours'`
(365 × 24), which are exact durations in any zone. A leap day doesn't
stretch the year either. cc4_1's `'24 hours'` was already exact. The
just-outside cases catch a regression on a cluster whose zone observes DST.
On a UTC cluster, such as CI's, `'90 days'` and `'2160 hours'` agree, so
there the cases can't tell them apart.

## D3 — cc6_3, cc6_5, cc9_1 are existence checks, so no per-entity rank

The dispatcher's brief asked for cc4_1's row discipline: exclude rows with no
entity key, take the latest per entity with `rank()`, and fail a tied false.
Exclusion applies: rows without a `review_id` (cc6_3) or `filename` (cc6_5,
cc9_1) are left out, as are empty strings. Cases
`record-without-*-is-left-out` show that a keyless fresh record doesn't
rescue a stale one.

The rank does not apply. These checks ask whether _any_ record is inside
the window, not whether every entity's latest state is good. "Any keyed
record in the window" is the same answer as "any entity's latest record in
the window": the latest is the newest, and a tie has equal `observed_at`.
A rank would add a window function and change nothing. The verdict is
still three-valued:

| Records                                                     | Verdict                   |
| ----------------------------------------------------------- | ------------------------- |
| none keyed                                                  | inconclusive              |
| any with `observed_at` in [instant − window, instant + 1 h] | pass                      |
| else, any more than 1 h after the instant                   | inconclusive (unreadable) |
| else, all older than the window                             | fail                      |

`observed_at` is a typed column, so it needs no shape guard, but the +1 h
cap still applies. Ingest admits `observed_at` up to 24 h ahead of receipt
(`MaxObservedAtSkew`), and a replay can see records stamped after its as-of
instant.

The engine's freshness window still bounds what the query sees: 120 days for
quarterly, 400 for annual. A record between the SQL window and the freshness
window fails the control. One older than the freshness window is not seen,
so with nothing else the control is inconclusive. The descriptions state
both bounds, and the cases `*-older-than-*-window-is-not-seen` pin the
cliff. Whether those freshness windows are meant as grace is #2816's
question, not this ticket's.

## D4 — cc1_4 is per person, keyed on `person_id`

The old Rego was per record and keyed on `assignment_id`. Per assignment, a
person's previous annual assignment would stay stale (366–400 days)
alongside this year's completion and fail the control for up to 35 days
after every renewal. The control asks whether each person is trained within
a year, so the key is `person_id` and the latest completion decides. That
person-level reading is still received-records-only; every active user is
#2834 (ADR 0028 decision 4).

Ranking is `completed_at DESC NULLS LAST, observed_at DESC`, cc4_1's shape.
An unreadable `completed_at` sorts last, so a later record with a bad time
doesn't hide an older stale completion
(`later-record-with-unreadable-completed-at-does-not-hide-a-stale-completion`).

Per rank-1 row:

| Row                                                                                | State   |
| ---------------------------------------------------------------------------------- | ------- |
| `completion_source` present and not in {manual, csv, lms_connector}                | off     |
| `completed_at` readable and older than 365 d                                       | off     |
| source allowed and `completed_at` readable within [instant − 365 d, instant + 1 h] | current |
| otherwise (source absent, `completed_at` absent, malformed or > 1 h ahead)         | unread  |

A person is off if any rank-1 row is off, which is how a tied false fails
(`tied-latest-records-one-disallowed-fails`). The person is current if every
rank-1 row is current, and unread otherwise. Across people: any off fails,
all current passes, otherwise inconclusive.

## D5 — A disallowed completion source fails; an absent one is inconclusive

Matt's rule for these checks is that absent is inconclusive and
present-but-not-true is off. A `completion_source` outside the allowed set,
including the empty string, is a completion the platform doesn't accept as
evidence, so it fails (`disallowed-completion-source-fails`,
`empty-completion-source-fails`). A record with no `completion_source` says
nothing about where the completion came from, so it is unread
(`absent-completion-source-is-inconclusive`).

The registered schema enumerates the source, so a disallowed value can't pass
ingest against the global schema (checked: `value must be one of 'manual',
'csv', 'lms_connector'`). The query checks it anyway because a tenant-private
schema of the same kind and version takes precedence over the global one
(`schemaregistry.Service.ValidatePayload`) and can widen the enum. The
bundle states its own allowed set rather than inheriting the registry's.

When the newest record is from a disallowed source, it fails the person even
if an older allowed completion is inside the window
(`latest-record-from-disallowed-source-fails-despite-earlier-allowed-one`).
The newest record is the person's current state. A disallowed record
appearing after a good one is a data problem the owner should see, not one to
paper over.

## D6 — Producer gaps, filed rather than worked around

Checking the producers' real payloads against the schemas surfaced two
gaps. Neither is fixed here: one belongs to the connector and the other to
the security-awareness domain writer.

- **#2940:** all three manual-connector paths (local, S3, SFTP) add payload
  fields that `manual.upload/1.0.0` (`additionalProperties: false`) rejects.
  I confirmed this by compiling the embedded schema in a throwaway test. So
  cc6_5 and cc9_1 receive nothing from the connector today, only
  schema-conforming direct pushes. The local path also re-stamps
  `observed_at` to the run hour on every re-run of an unchanged file, which
  would make an old file count as a fresh upload.
- **#2941:** `BuildCompletionEvidence` builds a record that nothing ingests,
  and no API surface calls `Complete` or the CSV import. So cc1_4's training
  query receives only directly pushed records. Once wired, the 24 h
  `observed_at` skew bound would refuse back-dated completions, since the
  writer sets `observed_at` to `completed_at`.
- **#2924 (routing, in flight):** I commented there that `manual.upload.v1`
  must stay bound. If it were routed by kind, one upload would satisfy both
  cc6_5 and cc9_1, because neither reads the upload's content.

The bundle fixtures use the shapes the schemas accept, which are the shapes
the check will see once those tickets land.

## D7 — The record's `result` is not used

Same as cc4_1. The platform writes access-review and training records as
pass, and the manual connector writes uploads as inconclusive "for a person
to judge". A roll-up of those verdicts made cc6_5 and cc9_1 permanently
inconclusive. Each query ignores `result` (cases `record-result-is-not-used`
on cc6_3 and cc1_4, and the inconclusive-result fixtures throughout cc6_5 and
cc9_1).

Not added: a `users_reviewed >= 1` condition on cc6_3. The platform refuses a
campaign with no items, and the ticket asks only for a completed review.

## D8 — cc1_4's Jira query and the #2818 two-kind test

cc1_4 keeps its Rego roll-up over `jira.ticket_evidence.v1` (#2829 owns its
payload check). The control combines the two queries with the engine's
precedence. With no Jira record that query is inconclusive, so the training
query decides. With a Jira pass, a failing training query still fails the
control (`closed-jira-ticket-does-not-cover-a-stale-completion`).

`TestStockTwoKindControls_MissingKindOnlyTurnsNAInconclusive` (#2818)
evaluated each two-query control without a database, which a SQL query
can't do. It now still counts cc1_4 among the two-query controls, so its
roster assertion is unchanged, but it skips evaluating it, the same way
`TestStockControlsKindFilterChangesOnlyCrossKindReads` already skips SQL
bundles. cc1_4's cross-kind behaviour is covered by its bundle cases, which
run against Postgres.

## D9 — Supersedes all four stock bundles

Each query's language, expression and id change, so each bundle is
superseded by a new version on the next boot (#1908). Owners, evidence,
evaluations, exceptions and risk links stay on the old row and have to be
moved by hand (#2820 D1). The PR carries the `supersedes-stock` label (#2891),
and the changelog fragment carries the upgrade note. cc6_3's title changed
from "Quarterly access review completed for in-scope systems" to "An access
review was completed within the last 90 days", so it doesn't claim
in-scope-system coverage (#2834).

`internal/api/scfimport/stock_manifests_gen.go` gains the four new
manifests' hashes. Regenerating also dropped one cc4_1 hash
(`49e33255…`): #2932's first head, which squash-merging left out of
`main`'s history. I put it back by hand. Removing a stock hash is a
behaviour change, and it is outside this ticket.

**Upgrade note:** on the first start after upgrading,
`soc2_cc6_3_periodic_access_review`, `soc2_cc1_4_competence_training`,
`soc2_cc6_5_asset_disposal` and `soc2_cc9_1_business_continuity` are each
superseded by a new version. Re-assign owners, and move exceptions and risk
links to the new versions. The old versions keep their history. What can
change:

- cc6_5 and cc9_1 were always inconclusive. They now pass on a recent
  upload and fail on a stale one.
- cc6_3 can now fail with a review 91–120 days old, where it used to pass.
- cc1_4 can now fail with a completion 366–400 days old, or one from a
  disallowed source. It can go inconclusive on a completion with no readable
  `completed_at`.
