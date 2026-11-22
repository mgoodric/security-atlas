# #2958 — Risk hierarchy links and unlinks are audited — decisions log

Before this change no path that attaches or detaches a child risk wrote a
`risk_audit_log` row. Migration
`20261295000000_risk_audit_hierarchy_links.sql` adds `child_linked` and
`child_unlinked`. This log does not block merge.

- detection_tier_actual: manual_review
- detection_tier_target: integration

(The gap was found in review of PR #2953, not by a test.)

## D1 — The owning layer writes the row: `LinkChildrenTx` / `UnlinkChildTx`

Both take a `risk.HierarchyAudit{Actor, Detail}` and write the row in the
caller's transaction, so REST (`Store.LinkChildren` / `UnlinkChild`), the
MCP appliers and the risk import (via `LinkChildrenTx`) cannot link without
logging. An empty actor is refused (`ErrTransitionInput`) before any query.

## D2 — One row, on the parent

The ticket asks for one row per link. The row goes on the parent's trail
(`risk_id` = parent) with `child_risk_id` in the state: the parent's
roll-up is what the link changes. The child's own History does not show
the link; its parents are on the hierarchy panel. A second row on the child
would double every link in the unified log.

## D3 — Row shape

`child_linked`: before `{child_risk_id: null}`, after `{child_risk_id}`.
`child_unlinked`: the reverse. This mirrors `control_linked` /
`control_unlinked` (#2106), so the unified log's `changed_fields` reads
`[child_risk_id]` and History renders one "Child risk" line.
`Detail` keys (an import's batch, row, a note) are added to the after state
and can never override `child_risk_id`.

## D4 — A re-link writes nothing

`LinkRiskAggregation` is `ON CONFLICT DO NOTHING`. Under the hierarchy
lock `LinkChildrenTx` reads the parent's existing links and skips children
already linked, so a repeated POST is a no-op in the audit trail too. No new
query was needed (`ListRiskChildLinks`).

## D5 — Out of scope: rule-made and aggregate-parent links

Links an aggregation rule makes (`aggrule.Engine`) and the links
`POST /v1/risks/aggregate` writes for the parent it creates do not go
through `LinkChildrenTx` and are not logged by this change. The ticket
names REST, MCP and import only. The aggregate parent's `created` row
(#2835) does not list its children, and a rule link only carries `rule_id`
on the link row, so both remain unaudited as link events; that is a
follow-up if wanted, not part of this ticket.

## D6 — Import links: the deciding approver, and a note when settled

The import (#2846) links through `settleLink`, which now takes the
`HierarchyAudit`. The actor is the approver of the decision that makes the
link: the child row's own approval, or, for a row waiting on a pending
parent row, the approval of that parent row. Detail is `import_batch_id`
and `import_row_id` (the child's row). A link settled by the parent row's
decision also carries `settled_by_import_row_id` and a `note` naming that
row's number. A link the hierarchy rules refuse rolls back its savepoint,
audit row included.

## D7 — History names the child by title

`child_risk_id` is an id-valued field, so History resolves it (#2138: no id
is shown) through a new `risk` ref kind, read from the register
(`fetchRisksListWithRollUps`) only when an entry names one. A child no
longer in the register reads "Unknown risk".
