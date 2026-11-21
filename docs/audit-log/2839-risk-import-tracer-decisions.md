# Issue #2839 — Risk register import tracer: CSV via API and CLI, approved by a person

The first slice of spec #2812, modelled on the controls import
(`1779-control-import-staging-decisions.md`,
`1782-control-import-approval-decisions.md`). It builds on #2838 (the
organisation risk ID, `risks.external_ref`) and #2835 (the `created` risk
audit row).

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — One transaction per row: `risk.Store.CreateTx`

**Decision.** `risk.Store.Create`'s body is split in two. `prepareCreate` does
the checks that read nothing, and `insertPrepared` writes the risk in a given
transaction. `Create` wraps them in its own transaction, as before. The new
`CreateTx(ctx, q, tenantID, in)` runs the same two steps inside the caller's
transaction. Approving a row locks it, checks for a duplicate, creates the
risk, records the decision and writes the import audit row, all in one tenant
transaction.

**Why.** The ticket and the #2835 follow-up comment say an imported risk is
created through `Store.Create`, or with `RecordCreatedTx` in the importer's own
transaction, so every create-time rule and the `created` audit row apply. A
transaction-taking variant of `Create` keeps one create path. It also makes
the row decision atomic with the risk, which the controls import got from
`control.UploadTx` (#1782 D1). The after-write hook (the aggregation engine)
runs in that transaction too, because the import is given the router's
`risksStore`.

## D2 — The actor is the approving person

**Decision.** `Actor`, `Proposer` and `decided_by` are the approver's users(id),
resolved with `credstore.CallerUserID`. `ProposerIsPerson` is true.

**Why.** This is the #2835 comment's instruction. The uploader stages rows but
creates nothing. The person whose decision makes the risk is the one named on
its `created` row.

## D3 — Approve all pending; reject one row

**Decision.** The ticket's approval surface is `POST
/v1/risks/imports/{id}/approve`, which approves every pending row, and `POST
/v1/risks/imports/{id}/rows/{row}/reject` with a reason. Each row is decided
in its own transaction, as the controls import's approve-clear does (#1783).
A row that fails for its own reason (decided meanwhile, a category archived
since upload, a value the risk store refuses) is reported as skipped and
stays pending. Per-row approve is left to a later slice, as the spec's user
story 39 allows.

`/approve` is the path's last segment, so the authz middleware derives
action `approve` on `risks`, which admin and grc_engineer hold. `/reject` is a
`write`.

## D4 — Approver gate: `authz.IsTenantApprover` plus a person

**Decision.** Approve and reject require `authz.IsTenantApprover(cred)` and
`credstore.CallerUserID(cred)`. The same gate refuses a machine credential
that carries the approver flag.

**Spec tension, recorded rather than changed.** Spec #2812's test list says
"a GRC engineer allowed upload and refused approval". `IsTenantApprover`
admits a live `grc_engineer` grant (#2428), and the ticket's criterion is "a
tenant approver who is a person", so a person with a live grc_engineer grant
can approve. A credential that carries `grc_engineer` only as a JWT role claim
is refused, as on every other approver gate. Making grc_engineer unable to
approve an import would mean a gate unlike the acceptance and posture-target
gates, so it was not made here.

## D5 — The organisation risk ID is optional

**Decision.** A row without a risk ID stages and imports. Duplicate detection,
in the file and against the tenant, applies only to rows that carry one.

**Why.** `risks.external_ref` is optional (#2838), and the spec makes the ID
the duplicate key only "when set". Requiring it would reject registers that
never had IDs. As a consequence, re-importing such a row creates a second
risk. The re-import guarantee (never update an existing risk) holds either
way.

## D6 — Required fields: title and category

**Decision.** A header row is found when it has a title column and a category
column, with at least two recognised headers in the first five rows (as in the
controls import). A data row without a title or a category is rejected
`missing_required_field`.

**Why.** `POST /v1/risks` requires both. A missing category is not defaulted:
spec Q13 says an unknown category is never invented silently, and defaulting
would be the same thing.

## D7 — Category matching at upload, by slug or name

**Decision.** At upload the row's category value is matched to an active
category of the tenant, by exact slug and then by display name
(case-insensitive, trimmed). An archived category, a name that two active
categories share, and an unknown value all reject the row `unknown_category`,
and the detail lists the valid slugs. The staged row stores the slug, and
approval passes it to the risk store, which re-resolves it under its share
lock (#2233).

**Why.** The ticket says to match "by exact slug or name in this slice". The
reviewer sees the rejection when the batch is staged, not later at approval.

## D8 — Review due: a date or an RFC 3339 timestamp

**Decision.** A review-due cell is `YYYY-MM-DD`, or an RFC 3339 timestamp
reduced to its UTC date. Anything else rejects the row `invalid_review_due`.
The risk's `review_due_at` is midnight UTC on that date.

**Why.** The risk export writes `review_due_at` as a timestamp. Accepting it
lets an export re-import with no column map.

## D9 — Unmapped columns

**Decision.** Every column no field takes is kept on the staged row as
`{header: value}` and listed in the batch's `unmapped_columns`. That includes
the second of two columns that alias the same field (the export's
`category_name` next to `category`). A column with a blank header is kept as
`column N` when any data row has a value under it. A repeated header gets
`(column N)` appended.

**Why.** The ticket says unmapped columns are kept and never written into the
description, and the spec says nothing is silently thrown away. The controls
import drops blank-header columns, which would lose a register's values.

## D10 — The column cap applies to CSV

**Decision.** The CSV reader refuses a record wider than 200 columns or a file
of more than 5000 rows while it reads. The XLSX path uses `xlsxbound`'s caps,
as the controls import does.

**Why.** The ticket caps columns for uploads in general. The controls import
enforces its column cap on XLSX only.

## D11 — Not in this slice

- **Unified audit log.** `risk_import_audit_log` is not yet a kind in
  `internal/audit/unifiedlog`. The ticket asks for the audit row, which is
  written and asserted. Adding the kind is a follow-up.
- **XLSX over HTTP.** The parse path accepts XLSX and the unit test covers
  it. The integration tests upload CSV only, as the ticket's tracer scope
  says.
- Owners, treatment, scores, themes, level, org units, parent links, value
  mapping and the web pages are later tickets under #2812.
