# #1748 — roll children up into an existing risk — decisions log

**Issue:** https://github.com/mgoodric/security-atlas/issues/1748

## The defect (detection tier)

`POST /v1/risks/aggregate` always created a new parent, and the rule engine
only linked children under the meta-risks it created. No path attached a risk
under a parent that already existed, so an organisation that imported its
enterprise register could not link team findings to the enterprise risks they
evidence. Unlinking, a child's parents, the roll-up on the parent, and a
parent column in the export did not exist either. Nothing prevented cycles;
that was safe only because every parent was brand new.

- **`detection_tier_actual`: `production`.** Found during a first-run adopter
  setup on 2026-11-12.
- **`detection_tier_target`: `integration`.** An HTTP test that attaches team
  risks under an existing company risk and reads both directions back catches
  it. Added in `internal/api/risks/children_integration_test.go`.

## Decisions

### D1: a children sub-resource, not a `parent_risk_id` on `/aggregate`

The issue's first suggestion was a `parent_risk_id` on
`POST /v1/risks/aggregate`. The triage brief asks for a children sub-resource
instead, and `/aggregate` keeps its create-a-parent contract unchanged
(idempotency key, frozen stored severity). The new routes are
`GET|POST /v1/risks/{id}/children`, `DELETE /v1/risks/{id}/children/{child_id}`
and `GET /v1/risks/{id}/parents`.

### D2: the cycle check runs before the level check

With strict level ordering a cycle needs a level that changed after a link was
made, which `PATCH /v1/risks/{id}` (#1749) allows. An ancestor of the parent
is normally at a higher level, so a level check first would report it as a 400
and the 409 the issue asks for would never be seen. The cycle check
(`RiskAggregationReaches`, a recursive descendants walk with `UNION` so it
terminates even over a graph that already holds a cycle) runs first. A risk
linked under itself is a cycle too, rather than reaching the table's
`no_self_parent` CHECK as a 500.

### D3: a missing or foreign child is a non-enumerating 404

RLS hides another tenant's rows, so a foreign id reads as missing. The issue's
"400 naming the offending risk" applies to the level rule (D2's checks name
the risk). For a missing child the response stays the non-enumerating
`one or more child risks not found` that `/aggregate` uses (slice 053 AC-10),
so the API never confirms whether an id exists in another tenant.

### D4: children must be scored on the 5x5 grid

`/aggregate` refuses a child whose methodology is not aggregable
(`nist_800_30`, `qualitative_5x5`). Linking applies the same rule, so every
manual child contributes to the roll-up. A FAIR child is a 400. Rule-made links
are unchanged, and a child without a 5x5 score (possible through older or
rule-made links) is skipped in the roll-up rather than failing the read.

### D5: the roll-up is computed on read and only on `GET /v1/risks/{id}`

The roll-up (`severity`, `severity_function`, `child_count`,
`assessed_severity`, `exceeds_assessed`) is computed from the current children
with the parent's stored severity function, else `max`. It is never stored, and
linking never writes the parent row, so the stored `inherent_score` and
treatment are byte-for-byte unchanged (tested). It is on the single-risk read
and the children listing, not on `GET /v1/risks`: per-row roll-ups would be one
query per row on the list, and the list's contract golden stays unchanged
(`rollup` is `omitempty`). `child_count` counts the children that contributed a
score. `exceeds_assessed` is only set for a parent scored on the 5x5 grid,
since a FAIR parent's dollar exposure is not comparable.

### D6: a rule-made link can be detached by hand

`DELETE /v1/risks/{id}/children/{child_id}` removes any link, manual or
rule-made. The rule engine re-links its own children on its next evaluation,
which is its documented behaviour (#1861). Re-attaching a child already linked
by a rule is a no-op and keeps the rule's `rule_id`.

### D7: the rule engine drops an ancestor of its meta-risk

Hand links can now place an existing meta-risk under one of the rule's
candidates. The engine looks the meta-risk up before filtering candidates and
drops any candidate that reaches it, so the candidate counts toward neither the
thresholds nor the score, and `unlinkStaleChildren` removes a rule link that
would otherwise close the cycle. A new meta-risk has no ancestors, so a first
fire is unchanged. The engine's narrow writer gains one read method
(`RiskAggregationReaches`); its write set is unchanged.

### D8: two new MCP tools, behind the same approval

`link_child_risks` and `unlink_child_risk` file proposals like the other write
tools; nothing is written until a human confirms. The appliers call
`risk.LinkChildrenTx` / `risk.UnlinkChildTx`, the same functions the API uses,
inside the confirm transaction. A refusal maps to the API's status with a code
(`hierarchy_validation_failed` 400, `hierarchy_cycle` 409, `risk_not_found` /
`link_not_found` 404), rolls back, and leaves the proposal pending. The
`mcp_wp_tool_name_check` CHECK gains the two names in
`20260831000000_mcp_risk_link_tools.sql`. Its down migration re-adds the
narrower CHECK as `NOT VALID`, so a rollback never deletes a filed proposal (the
AI-assist audit trail).

### D9: links are serialized per tenant by an advisory lock

Two concurrent links could each pass the cycle check on a snapshot that lacks
the other. Strict level ordering alone does not rule that out:
`PATCH /v1/risks/{id}` (#1749) already lets a risk change level after it is
linked, so levels can disagree with existing links today, and rule-made links
do not check levels at all. Every path that adds a link therefore takes
`pg_advisory_xact_lock(hashtextextended('risk_hierarchy:' || tenant_id, 0))`
(`LockRiskHierarchy`) before its cycle check: `LinkChildrenTx` (so the API
and the `link_child_risks` applier) and the aggregation engine. The engine
takes it at the start of `LockRules` and of `Evaluate`, before any meta-risk
key lock, and only when at least one rule will be evaluated. Taking it just
before `linkChildren` would put it between two rules' key locks and could
deadlock against a transaction holding the second key. Unlinking takes no
lock, since removing an edge cannot close a cycle.

The cost is that links, and risk writes that trigger rule evaluation, are
serialized within a tenant. A link request is capped at
`risk.MaxChildrenPerLink` (100) children to bound how long one request holds
the lock. A parent or child deleted between the reads and the insert fails the
composite FK (23503), which is reported as the same non-enumerating 404 rather
than a 500.

### D10: the export appends `parent_risk_ids`

The column is appended after `acceptance_status` so existing column positions
hold (the #1746 precedent), semicolon-separated, empty when there are none. It
is filled from one tenant-wide query over `risk_aggregations`, not a query per
row.
