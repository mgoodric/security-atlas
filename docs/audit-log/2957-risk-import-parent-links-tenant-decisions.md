# Issue #2957 — Risk import parent links: stranded waiting rows, cross-batch parents, `parent_risk_ids`

A follow-up to #2846 (PR #2953), with the `parent_risk_ids` round trip from
#2898 (PR #2977, D6) added in the ticket's comments. It builds on the
hierarchy audit of #2958 (PR #2959): every link here goes through
`risk.LinkChildrenTx` with the deciding person as the actor.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — A child waits on a pending row of any import of the tenant

**Decision.** The ticket offers two options for a parent that is a pending
row of another batch: wait across batches, or document a manual link. This
takes the first. The pending-parent check and the scan for waiting rows
are filtered on the tenant, not the batch. A decision on a row carrying an
ID settles the rows of every batch waiting on that ID.

**Why.** Splitting one register across two uploads is ordinary, and the
manual-link option leaves the user to rebuild by hand a hierarchy the file
already states. The locking needs no change. The hierarchy advisory lock
was already per tenant, so the #2953 order holds as it was: the deciding
row `FOR UPDATE`, then the advisory lock, then the waiting rows
`FOR UPDATE`. A waiting row is already approved, so no decision takes it as
its own row and waits for the advisory lock after it. The only path that
locks a waiting row first is `lockPendingRow` on a decided row, which
returns `ErrRowDecided` without taking the advisory lock. The #2953
READ COMMITTED note still applies.

The waiting index moves from `(batch_id, lower(parent_ref))` to
`(tenant_id, lower(parent_ref))`, and the pending-parent check gets a
partial index on `(tenant_id, lower(external_ref)) WHERE status = 'pending'`.

## D2 — A rejected parent row re-checks rather than failing outright

**Decision.** When the row carrying a waiting child's parent ID is
rejected, the child is re-checked through the same resolution as a fresh
approval. If another pending row (in any batch) still carries the ID, it
keeps waiting. If none does, it fails as unknown.

**Why.** IDs are unique within a batch (`risk_import_rows_batch_external_ref_key`)
but not across batches. Once waiting crosses batches, a rejection in one
batch must not fail a child that another batch's row can still satisfy.
`TestRiskImport_ParentLinkAcrossBatches` covers it.

## D3 — The backstop runs when a batch completes, the only end a batch has

**Decision.** The ticket asks to fail still-waiting rows "when a batch
completes, closes or expires". A batch has two states, `staged` and
`completed` (`risk_import_batches` CHECK), so there is nothing to hook for
close or expiry. When a decision completes its batch (the last pending row
is decided), `failStranded` runs in that transaction, under the hierarchy
lock. It re-checks each of the batch's waiting rows. A row whose parent is
now a risk is linked, attributed to the completing decision's approver. A
row whose parent ID is still on a pending row of another batch keeps
waiting. Any other row fails with `still waiting on parent risk ID "<id>"
when this import completed: ...` and the `parent_link_failed` flag.

**Why.** Each decision settles the rows waiting on its ID, so no decision
path leaves a row stranded in its own batch by the time the batch
completes. A parent row that is skipped stays pending, and the batch stays
staged. The backstop exists so that a row stranded some other way (here, a
parent row removed out of band) fails visibly instead of waiting for ever.
The integration test forges that state by deleting the parent row as the
owner role, because no API path produces it.

**Not built: close and expiry.** A batch nobody finishes keeps its parent
rows pending, and their children, in that batch or another, keep waiting.
The review page counts them under **Waiting on a parent**, and the docs
say to approve or reject the parent row. Adding a close or expiry state
to batches is a lifecycle change (status vocabulary, an endpoint or a
sweeper, UI) beyond this ticket. Raised in the PR as a possible follow-up.

## D4 — `parent_risk_ids` is an id field, resolved id by id at staging

**Decision.** `parent_risk_ids` is now a column-map field
(`FieldParentRiskIDs`) recognised by the export's header, and handled like
`owner_user_id` and `org_unit_id` (#2898): the cell is kept unmapped until
staging resolves it. The cell is split on `;`, as the export joins it.
Each id is looked up among the tenant's risks (`SELECT id FROM risks WHERE
tenant_id = $1 AND id = ANY($2)`, also bound by RLS). The ids that resolve
are staged once each on the row's new `parent_risk_ids uuid[]` column. The
unmapped value keeps only the ids that did not resolve, and is dropped
when every id resolved. A column no row still holds a value under leaves
the batch's unmapped columns, as the other id columns do.

This supersedes #2846's D5 ("the export's `parent_risk_ids` is not an
alias") as #2977 D6 anticipated. It is still not an alias of the parent
risk ID column. It is a separate id field with the uploading-tenant rule.

**Why.** The comment on the ticket names the rule: tenant-filtered lookup,
link through `LinkChildrenTx` with the approver as the audit actor, leave
non-resolving ids unmapped. Resolving at staging lets the review page show
which parents will be used. The link at approval goes through
`LinkChildrenTx`, which re-reads the parent, so a parent deleted after
staging fails on the row as unknown (`risk.ErrNotFound` → `linkRefusal`).

## D5 — The parent risk ID column wins over `parent_risk_ids`

**Decision.** A row that names a parent by organisation risk ID stages no
`parent_risk_ids`, and keeps that cell unmapped as written. A CHECK
(`parent_ref = '' OR cardinality(parent_risk_ids) = 0`) holds it.

**Why.** It is the same precedence #2898 gave `org_unit_id` against an org
unit name. The hand-written register's column is the one the user chose,
and mixing the two would make one row's outcome depend on two sources.

## D6 — Several export parents: linked when every link is made

**Decision.** The export lists every parent a risk rolls up to, so a row
can stage several. The risk is linked under each in turn, each in its own
savepoint. The row is `linked` when every link is made, with
`parent_risk_id` the first parent in the cell's order. It is `failed` when
any link is refused, with each refusal's reason joined by `; `. The links
that were made stay.

**Why.** `parent_link_status` and `parent_risk_id` hold one outcome per
row (#2846 D4). A partial failure has to show on the row, so it is
`failed` and flagged. Rolling back the links that succeeded would discard
correct hierarchy to report the one that is wrong. `risk_aggregations` is
the record of what is linked, and the error names each refused parent.

## D7 — Audit detail names the settling batch when it differs

**Decision.** A waiting row settled by a decision in another batch writes
its `child_linked` row with the child's own `import_batch_id` and
`import_row_id`, plus `settled_by_import_row_id`,
`settled_by_import_batch_id` and the note "linked when the parent's row N
of another import was decided". Within one batch, the #2958 shape is
unchanged.

**Why.** Without it, a cross-batch settlement would audit a row number
from a batch the record never names.
