# Issue #2846 — Risk import: parent links from a parent-risk-ID column

The hierarchy slice of spec #2812 (Q17, Q29), on top of the tracer (#2839)
through placement (#2845).

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — The link is made in the decision's transaction, through `risk.LinkChildrenTx`

**Decision.** When a row is approved, its risk is created, and then, in the
same transaction, it is linked under its parent by `risk.LinkChildrenTx`.
That is the function behind `POST /v1/risks/{id}/children` and the MCP
`link_child_risks` applier. The call runs inside a savepoint, so a refusal
rolls back only the link. A refusal (`ErrAggregationCycle`, `ErrChildLevel`,
`ErrIncompatibleMethodology`, or a parent or child removed meanwhile) is
recorded on the row as `parent_link_status = failed` with a reason, and the
row is flagged `parent_link_failed`. The row stays approved and its risk
stays created.

**Why.** The ticket asks for the existing path and its rules, and says a bad
link must not cost the risk. Restating the rules in the importer would let
the two drift apart. The savepoint is needed because a foreign-key failure
inside the link would otherwise abort the whole decision.

## D2 — A child whose parent is a pending row of the batch waits

**Decision.** At approval, the parent ID is looked up first among the
tenant's risks, which include those this batch already created. If it is not
there but a pending row of the batch carries it, the child is recorded as
`waiting`. When a row is decided, the rows waiting on its ID are settled:

- **approved:** linked under the new risk;
- **duplicate:** linked under the existing risk the duplicate names;
- **rejected:** failed as an unknown parent.

If the ID is neither a risk nor a pending row, the link fails as an unknown
parent.

**Why.** Approve-all runs in file order, and an approver can decide rows one
at a time, so a child is often approved before its parent. Failing that
child would make the outcome depend on row order. Waiting resolves the link
when the parent row is decided, whatever the order.

## D3 — Every decision that may make or settle a link takes the hierarchy lock before its link reads

**Decision.** An approval of a row with a parent ID or an organisation risk
ID, and any duplicate or rejection of a row with an organisation risk ID,
takes `LockRiskHierarchy` after the row is locked and decided (and, on
approval, after the risk is created), and before the link reads: the parent
lookup, the pending-row check and the scan for waiting rows. That is the
tenant-scoped transaction advisory lock every link path already uses. The
decision's earlier reads are not covered by it, and do not need to be.

**Why.** If a child and its parent are approved at the same time, the child
could read the parent row as pending while the parent's transaction reads no
waiting child. The child would then wait forever. The import's transactions
run at READ COMMITTED, the server default, where each statement takes a
fresh snapshot. So the second transaction to take the lock runs its link
reads after the first commits, and one of them always sees the other.

**This depends on READ COMMITTED.** Under REPEATABLE READ or SERIALIZABLE
the snapshot is taken at the transaction's first statement, before the lock,
and the race returns. A comment at the lock site in `approve.go` and in
`parentlink.go` says so. Raising the isolation would need the lock moved to
the transaction's first statement.

## D4 — One parent per row

**Decision.** The cell holds one organisation risk ID. A cell such as
"R-1; R-2" is read as that literal ID, matches nothing, and fails as an
unknown parent.

**Why.** The spec and the ticket speak of a parent-risk-ID column. A register
with several parents per risk is rare, and the failure is visible on the
row, so the user can link the extra parents by hand. Splitting on separators
could be added later without changing the staged shape.

## D5 — The export's `parent_risk_ids` is not an alias

**Decision.** `parent_risk_ids` in the risk export holds the parents' uuids
in the exporting tenant, so it stays unmapped, as `org_unit_id` does (#2845).

**Why.** In another tenant, or after the parent is re-imported, a uuid names
nothing. Importing it would make every exported child fail as an unknown
parent.

## D6 — Cycles arise from a self-reference or from links made earlier in the batch

**Decision.** No separate in-file cycle check exists. A row naming itself is
refused by `WouldCycle` (child equals parent). A loop across rows is refused
when its closing link would make a risk its own ancestor.

**Why.** The strict level ordering already rules out most loops: in any
cycle at least one link fails the level rule. The cycle check runs before
the level check, so a loop is reported as a cycle on the row whose link
would close it. The integration test pins both cases.

## D7 — An imported link writes no risk audit row, like every other link path

**Decision.** A parent link made by the import writes no `risk_audit_log`
row. The import's own audit row for the row's decision
(`risk_import_audit_log`) and the row's `parent_link_status` record what
happened.

**Why.** No link path writes a hierarchy audit row today.
`POST /v1/risks/{id}/children`, the MCP `link_child_risks` applier and
unlink all run `risk.LinkChildrenTx` / `UnlinkChildTx`, which write none.
`risk_audit_log_action_type_check` has no hierarchy action. Writing one from
the import alone would audit imported links and leave links made in the app
unaudited: a second mechanism next to the layer that owns links. The gap
belongs to the risk store and is tracked in #2958 (add
`child_linked` / `child_unlinked` in `LinkChildrenTx`, with an actor, for
every path). When #2958 lands, the import inherits the row through
`LinkChildrenTx`. It should pass the deciding approver as actor, and note
when a waiting link was settled by the parent row's decision.
