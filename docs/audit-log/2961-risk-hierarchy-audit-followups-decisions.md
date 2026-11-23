# #2961: Risk hierarchy audit for rule links, aggregate links, delete cascades and child History (decisions log)

These are the follow-ups to #2958 (its D2 and D5). Every path that adds or
removes a `risk_aggregations` row now writes a `child_linked` or
`child_unlinked` row on the parent's trail. A child's History reads those
rows from the parent's trail, and no second row is written. This log does
not block merge.

- detection_tier_actual: manual_review
- detection_tier_target: integration

(Reviewers of #2958 found these gaps and recorded them as its D5. No test
caught them.)

## D1: The aggregation engine shares the audit-row writer, not `LinkChildrenTx`

The ticket said to route rule links through `LinkChildrenTx`. That can't
be done without changing what rules do, so the dispatcher agreed to this
alternative on 2026-11-22:

- The engine is built on its narrow `metaRiskWriter` (its safety bound,
  ISC-23). `LinkChildrenTx` needs a `*dbx.Queries`.
- Every link `LinkChildrenTx` writes has `rule_id = NULL`. Rule links
  carry `rule_id`, and `unlinkStaleChildren` and the "rule" link source
  depend on it.
- `MaxChildrenPerLink` (100), the level rule and the methodology rule
  would refuse windows and candidates the engine accepts today. The
  engine runs inside the triggering risk write, so a refusal there would
  also fail that write.

The row-writing step is shared instead. `risk.RecordChildLinkedTx` and
`RecordChildUnlinkedTx` take a `risk.AuditWriter`, which the engine
already had for its `created` row (#2835). `LinkChildrenTx`,
`UnlinkChildTx` and the engine all call them, so the rows have one shape
and one empty-actor refusal. The engine keeps its own bounds and
`rule_id`. Its writer gains no new method.

The actor is the existing `aggrule.RuleActor(ruleID)`,
`system:aggregation-rule:<id>`. The ticket's example,
`system:aggrule:<rule_id>`, would have given one rule two actor names.

## D2: The engine audits only new links

Before writing, `unlinkStaleChildren` reads the meta-risk's links once.
It writes `child_unlinked` for each stale link of this rule that it
removes, and returns the links that remain, whatever their source.
`linkChildren` then skips a child that is already linked, so a
re-evaluation over the same window writes nothing. That matches #2958 D4.
The skip doesn't change behavior, because `LinkRiskAggregation` was
already `ON CONFLICT DO NOTHING`. A child already linked by hand or by
another rule keeps that link's `rule_id`, as before. The evaluation holds
the hierarchy lock and the meta-risk key lock, so the read can't go stale
before the inserts. A meta-risk the evaluation has just created has no
links, and all of its links are audited.

## D3: `POST /v1/risks/aggregate` goes through `LinkChildrenTx`

The parent is created first and its `created` row written (#2835). The
children are then linked through `LinkChildrenTx` with the caller as
actor, so each link writes `child_linked` after the `created` row in the
same transaction. Two rules are new on this route:

- each child's level must be strictly below the parent's level;
- at most 100 children per request.

Both return 400 (`ErrChildLevel`, `ErrTooManyChildren`) and roll back the
parent. This is a behavior change for API clients, recorded in
`changelog.d/2961-2.changed.md`. The cycle check can't fire, because a
parent created in the same transaction has no ancestors.

`Aggregate` takes the hierarchy lock as its first statement, before the
idempotency lookup and any row write. That keeps the #2953 order
(hierarchy before any meta-risk key or row lock), and `LinkChildrenTx`
re-enters the lock. Two concurrent identical aggregates are now
serialized too.

## D4: Delete drops the links itself, under the hierarchy lock

`risk_aggregations` cascades on both FKs, so a delete used to drop a
risk's links without audit. `Store.Delete` now runs
`DeleteRiskAggregationLinksForRisk ... RETURNING` before `DeleteRisk`,
then writes one `child_unlinked` row per returned link, by the deleting
admin:

- **A deleted child:** the row goes on each surviving parent.
- **A deleted parent:** the row goes on the deleted parent's own trail,
  one per child. The trail outlives the risk (#2107), and the child's
  History reads it from there (D5).

Each row carries `note: "unlinked because a risk was deleted"`, so
History tells it apart from an unlink made by hand.

The delete takes the hierarchy lock before the rules' locks. Every
link-adding path holds that lock, so no link to the risk can be added
between the `RETURNING` delete and the risk's delete. Without the lock
there is a gap: `LockRiskByID` is `FOR NO KEY UPDATE`, which does not
block a linker's `FOR KEY SHARE`, so a link committed in that gap would
be dropped by the cascade without a row. A concurrent unlink can't
double-write either: the `RETURNING` set contains only rows this
statement actually deleted.

## D5: Child-side History is a read, not a second row

`ListRiskAuditLog` now also returns `child_linked` and `child_unlinked`
rows whose state names this risk as the child:
`COALESCE(after_state->>'child_risk_id', before_state->>'child_risk_id')`.
JSON null reads as SQL NULL, so the COALESCE picks the side that holds
the id. Migration `20261299000000` adds a partial expression index on
`(tenant_id, that COALESCE)` where the action is one of the two, so the
read does not scan the tenant's hierarchy rows.

The store sets `ParentRiskID` when a row's `risk_id` is not the listed
risk. The wire adds `parent_risk_id` only on those rows (`omitempty`).
The web labels them "Linked under a parent risk" or "Unlinked from a
parent risk", and replaces the "Child risk" line, which would name the
risk itself, with "Parent risk", resolved to its title through the
existing `risk` ref. The page limit and `truncated` apply to the combined
list.

## D6: Same-transaction rows share `created_at`

Rows written in one transaction (an aggregate's `created` row and its
`child_linked` rows, or a delete's unlinks and its `deleted` row) share
`created_at = now()`, and `id` is random. History's order among them is
therefore not fixed. Tests assert on the set of actions, not their
order. Fixing that order is out of scope.
