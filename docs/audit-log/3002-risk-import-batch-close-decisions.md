# Issue #3002 — Risk import: close an abandoned batch; no expiry

A follow-up to #2957 (PR #2998, D3). A risk import batch had two states,
`staged` and `completed`. A batch nobody finished kept its pending rows,
and every row of any batch waiting on them as a parent, open for ever.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — An explicit close, by a decider, that rejects the pending rows

**Decision.** `POST /v1/risks/imports/{id}/close` with a required
`{"reason"}`. In one transaction it:

1. rejects every pending row of the batch with the reason
   `import closed: <reason>`, and writes a `reject` row to
   `risk_import_audit_log` for each one, by the closer;
2. settles the rows of every batch waiting on those rows (`settleWaiting`,
   the same path a per-row rejection takes);
3. sets the batch to the new status `closed`, with `closed_by`, `closed_at`
   and `close_reason`;
4. runs `failStranded` on the batch.

The gate is `decidingPerson`, the gate of approve and reject (#2881): a
person who is a tenant admin or holds the approver flag. The batch's
uploader is refused (`ErrSelfDecision`), as for a per-row rejection.

**Why.** A close is a bulk rejection. Every rejection is a person's
decision, so it goes through the same gate and leaves the same audit
trail: one `reject` row per row, with a reason. The batch-level columns
record the close itself. No new audit action is needed, and
`risk_import_audit_log`'s vocabulary is unchanged.

**Alternatives considered.**

- **Abandon without rejecting the rows** (leave them pending under a
  closed batch). Rejected. `settleLink` would still count them as pending
  parents, so the children would keep waiting. Every pending-row query
  would also need a batch-status join.
- **Let the uploader close their own batch.** Rejected for now. Closing
  decides rows, and #2881 keeps the uploader off every decision. A GRC
  engineer who wants to withdraw an upload asks an approver.

## D2 — No expiry

**Decision.** Batches do not expire. The import list says so
("Imports do not expire. One stays open until every row is decided or an
approver closes it."), and the list's pending and waiting counts make an
abandoned batch visible.

**Why.** An expiry would reject rows on a timer, and no person would have
decided those rejections. `risk_import_audit_log.actor` is a users(id),
never a machine credential (#2839), and #2881 narrowed decisions to
admins and approvers. A sweeper would need a system actor. That would be
the first machine-made decision on an import, so it would loosen the
decision rule rather than add a lifecycle step. It would also need a
cross-tenant background worker for one table. The close action plus the
visible counts covers the abandoned-batch case without either.

**Recommendation to Matt.** Keep no expiry. If abandoned batches still pile
up, the cheaper next step is a notification to the tenant's approvers
when a batch has been staged for N days with children waiting. That keeps
a person as the decider. A real expiry needs a decision on the system
actor first.

## D3 — The close's lock order is a decision's

**Decision.** The close takes its locks in #2953's order:

1. the batch's pending rows `FOR UPDATE`, in row order;
2. the tenant hierarchy advisory lock;
3. the waiting rows `FOR UPDATE` (through `settleWaiting` and
   `failStranded`);
4. the batch row, last, through the status `UPDATE ... WHERE status = 'staged'`.

The batch status and uploader are read first without a lock.

**Why.** A decision locks the batch row last, through `finishDecision`'s
completion `UPDATE`. If the close locked the batch first, a decision
holding a row and the close holding the batch would deadlock. In this
order a concurrent decision on one of the rows finishes first. The close
then skips that row, which is no longer pending under READ COMMITTED's
re-check. If that decision completed the batch, the close's status update
matches nothing, and the close returns `ErrBatchNotStaged` (409) and
rolls back. All pending rows are rejected before any waiting row is
settled, so a settle never sees one of this batch's rows as pending.

## D4 — `failStranded` takes how the batch ended

**Decision.** `failStranded` now takes a `batchEnd`: the batch, the verb
for the reason ("completed" or "was closed"), and the audit attribution
of a link it still makes. The completed text is unchanged. A row
stranded by a close reads
`still waiting on parent risk ID "<id>" when this import was closed: ...`.

## D5 — The import list

**Decision.** `GET /v1/risks/imports` returns the tenant's 100 newest
batches, newest first, with three counts:

- `pending_count`: the batch's own pending rows;
- `waiting_count`: its approved rows waiting on a parent row;
- `children_waiting_count`: approved rows of any batch waiting on its
  pending rows. This is what closing the batch settles.

`/risks/imports` shows the list under the upload form, with the no-expiry
note.

**Why.** The ticket asks for the counts "on the import list", and there
was no list. The upload page already is the import landing page (the
Risks page's Import button opens it), so the list goes there rather than
on a new route. The list is a fixed 100 with no pagination. A solo
security leader stages a handful of imports, and the counts matter only
for the recent ones.
