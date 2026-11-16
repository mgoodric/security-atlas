# Issue #1783 — Control import: bulk approve-clear

Part of #1734 (bring-your-own controls). Builds on
`1782-control-import-approval-decisions.md`.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Each row goes through `ApproveRow`

**Decision.** `Store.ApproveClear` selects the clear rows and calls
`ApproveRow` with `candidate_rank: 1` for each. There is no second approval
path.

**Why.** Single approval already covers what bulk approval needs: a person
must approve, the bundle-ID race guard, binding in the batch's SCF release,
the `c-` prefix for uuid-shaped IDs, duplicate marking and the audit row.
Bulk approval inherits all of it. Each approved row writes its own `approve`
audit row with the same actor, so the audit log shows a bulk approval as N
approvals. Each of those rows, and each `duplicate` row the run marks, carries
`reason = 'approve-clear'` (`BulkAuditReason`), so an auditor can tell which
decisions came from one bulk action. The reason is set through an unexported
`ApproveRowInput` field, so the API's single approve cannot claim it. The
column already allows a reason on any action, so no new audit action and no
migration were needed. This was required at review.

## D2 — One transaction per row

**Decision.** Each row commits in its own transaction. A row that fails for
its own reason is reported under `skipped`, with that reason, and stays as it
was. The reasons are: bundle ID taken, decided meanwhile, anchor not in the
release, no batch release, candidate gone, or a bundle the control store would
refuse. Any other error stops the run and returns 500. Rows approved before
the error stay approved, each with its control and audit row.

**Why.** If every row shared one transaction, one slug collision ("AC 01" and
"AC-01") would roll back the whole batch. The response reports approved,
duplicate and skipped rows separately, with per-row reasons.

## D3 — The batch is locked NOWAIT for the run

**Decision.** The outer transaction takes `SELECT ... FROM
control_import_batches ... FOR UPDATE NOWAIT` before it reads the clear rows.
It holds the lock while the per-row approvals run on other connections. If the
lock is already held, the request fails at once: SQLSTATE 55P03 maps to
`ErrApproveClearRunning`, and the API returns 409 "approve-clear already
running for this batch". `ApproveRow` never writes the batch row, so the lock
cannot block it.

**Why.** The first cut waited on the lock (`FOR UPDATE`). Review required
NOWAIT, because waiting approve-clears each hold a pool connection and could
starve the pool. With NOWAIT, a second approve-clear holds nothing and the
caller retries. A concurrent single approval is not blocked by the batch
lock. It is serialised by `lockPendingRow`'s row lock instead, and whichever
approval comes second sees the row decided. For approve-clear that row is
`skipped`; for the single approval it is a 409.

The outer transaction still lives as long as the run, since it is what holds
the lock. It does no writes, and only one can exist per batch.

Tests:

- `TestApproveClearRefusedWhileAnotherRuns` holds the batch lock from another
  transaction and expects a 409 with every row left pending. It also checks
  that a single approval still goes through during the lock.
- The race test runs two approve-clears and one single approval at once. It
  accepts either a 409 or a sequential run for the second approve-clear, and
  checks for exactly one control per row and one audit row per approval.

## D4 — What qualifies

A row qualifies when all of these hold:

- it is `pending`;
- it has at least one tested-under criterion;
- its rank-1 candidate has `source = 'crosswalk'`;
- that candidate's `reaches` covers every tested criterion, matched by
  `requirement_id`.

A rank-1 candidate that the LLM rerank moved up (#1781) keeps its `crosswalk`
source. It still qualifies when it reaches everything. The approval then
records `ai_assisted = true` with the person as `human_approver`, as a single
approval of that candidate would. The person chose to approve in bulk, and
because the candidate reaches every criterion, the LLM added only ordering
and a rationale, not coverage.
