# #2941 — training completions reach the evidence ledger (decisions log)

[#2941](https://github.com/mgoodric/security-atlas/issues/2941). Since
#2824, cc1_4 checks `security_awareness.training_completion.v1` in SQL.
The platform built that record (`BuildCompletionEvidence`) but never wrote
it: `ImportCompletionsCSV` returned it in the report, `Complete` did not
build one, and nothing called `SetEvidenceRecordID`. cc1_4 saw only records
a credentialed pusher sent directly.

Now `Store.WithIngest` wires an `ingest.Service`, as `internal/accessreview`
does (#1830). `Complete` and `ImportCompletionsCSV` write each completion's
record through `ProcessInTx` in the completion transaction and store its id
in `security_training_assignments.evidence_record_id`. A store without an
ingest service fails closed with `ingest.ErrUnavailable` and completes
nothing. `SetEvidenceRecordID`, which had no caller, is removed.

- detection_tier_actual: integration
- detection_tier_target: integration

The integration tests were run against the old `observed_at = completed_at`
and failed on the skew refusal the ticket predicted (`observed_at outside
permitted skew window: skew=-3253h…`), so they catch the bug.

## D1 — `observed_at` is the instant the platform records the completion

Options were the recording instant, with `completed_at` in the payload, or
a bounded exemption from ingest's 24-hour skew check.

Chosen: the recording instant. Reasons:

- cc1_4 reads the payload's `completed_at` for its 365-day window, and
  orders a person's records by `completed_at` and then `observed_at`. The
  completion's own date still decides the verdict. An old completion
  imported today is accepted and then fails cc1_4 when it is over 365 days
  old. `TestPlatformCompletionsReachCC14Evaluation` covers both cases.
- The skew check is replay protection on every ingest path. An exemption
  for one kind would be a second rule in ingest, and a reviewer would have
  to check that it holds for this kind only.
- `observed_at` then means what the ledger can show: when the platform
  learned of the completion. Audit-period freezing (invariant 10) draws on
  `observed_at <= frozen_at`. A completion imported after a freeze stays
  out of that period's population even if `completed_at` is earlier,
  because the record did not exist when the period froze. That is the
  intended behavior of freezing, not a gap.
- It matches the access-review writer, which uses the instant it writes.

The idempotency key changes from assignment id plus completion **date** to
assignment id plus the exact completion **instant**. With the date, a
second completion of one assignment on the same day would reuse the key
with a different payload. Ingest refuses that with `ErrIdempotencyMismatch`
and the completion would roll back. No record was ever written under the
old key, so nothing in the ledger is orphaned.

## D2 — Idempotency and backfill

- `Complete` at the same instant as the stored completion keeps the
  assignment's record when that record's `completed_at` matches. It does
  not write a second record.
- A CSV re-import of an already-complete row writes a record only when the
  assignment has none. That backfills completions recorded before this
  change, which is the only way the `evidence_record_id` column could be
  empty for a completed assignment.
- `Complete` at a different instant writes a new record. The old one stays
  in the ledger, which is append-only, and cc1_4 takes each person's latest
  `completed_at`.
- An ingest refusal inside a CSV import is an infrastructure error and
  rolls the whole batch back. The record is built by the platform from
  validated rows, so a refusal means a bug, not bad input.

## D3 — The kind stays bound, not owned

The #2873 model makes a kind owner-only: stored control-free, written only
by its handler, any push refused. `training_completion.v1` stays a bound
kind here. The platform writes it under a system credential scoped to the
kind (`ingest.SystemCredential`), and it links to cc1_4 by bundle id
(#2100).

Making it owned would refuse the pushes that today are cc1_4's only source.
It would also move the kind into `internal/evidence/unbound`, which changes
how every ledger reader resolves it. That is an outward change for existing
pushers, beyond this ticket's work, and it needs Matt's call. It is raised
in the PR, not done here.

## D4 — No HTTP surface in this change

The ticket allows exposing completion and CSV import over the API, or
recording why not. They are not exposed here:

- No `securityawareness` surface exists in `internal/api`: no routes, no
  authz bundle entries, no OpenAPI paths, no `web/` page. The access-review
  domain writer this change copies is in the same state.
- Adding one means new routes, OPA role decisions for who may record
  another person's completion, OpenAPI and drift checks, and a frontend.
  That is a feature with its own authorization questions, and it should be
  a claim or ticket of its own.
- This change makes the store correct first, so whichever surface lands
  next, an HTTP handler or the LMS connector path (D6 of
  `docs/audit-log/659-csv-completion-import-decisions.md`), writes the
  ledger without re-deriving it.

Until that surface lands, nothing in a running deployment calls `Complete`
or `ImportCompletionsCSV`. cc1_4 still sees only pushed records in
production. The follow-up is recommended in the PR.
