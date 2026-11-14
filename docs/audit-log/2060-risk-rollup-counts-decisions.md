# #2060 — roll-ups counted beside their children — decisions log

**Issue:** https://github.com/mgoodric/security-atlas/issues/2060

## The defect (detection tier)

`GET /v1/risks` returned roll-up parents with no filter. The register's count
was the row count, the dashboard's top-risks panel ranked parents beside the
children they summarise, and the board brief and board pack (both read
`ListRisksForBoardBrief`) could give a top-risk slot to a parent and to its
children. The same exposure was counted twice.

- **`detection_tier_actual`: `manual_review`.** Found in the UI design audit
  of 2026-11-14.
- **`detection_tier_target`: `integration`.** A list call over a tenant with a
  roll-up catches it. Added in
  `internal/api/risks/rollups_integration_test.go` and
  `internal/board/integration_test.go` (`TestRollUps_*`).

## Decisions

### D1: a roll-up is a risk with at least one child linked

Not "a risk created by `/aggregate`" or "a rule's meta-risk". #1748 lets any
org or company risk take children, and a meta-risk whose pattern lapsed has
zero children and summarises nothing, so it stays in the register (with its
"Pattern no longer met" badge). `risk_aggregations` is the one place that says
a risk is summarising others.

### D2: the API leaves roll-ups out by default; `?include_rollups=true` keeps them

Filtering in the API rather than in each page means the register, the
dashboard's top risks (`?sort=residual,age`), the sidebar's high-severity
count and the MCP `list_risks` tool all agree without each re-implementing the
rule. A malformed value is a 400, like the other list params. Included
roll-ups carry `child_count`, which drives the register's marker.

### D3: pickers ask for roll-ups

The attach-a-child picker on the risk detail page and the action-plan risk
picker call `fetchRisksListWithRollUps`. An org roll-up is a valid child of a
company risk, and a roll-up is a valid target for an action plan, so dropping
them there would remove choices the operator had before.

### D4: the export is unchanged

`GET /v1/risks/export` reads `ListRisks` directly and still lists every risk,
with `parent_risk_ids` naming each row's parents. An export is the full
register for offline review, not a count.

### D5: the board reads roll-ups as of its own date

`ListRisksForBoardBrief` leaves a risk out when a child was linked to it on or
before the brief's date (`risk_aggregations.created_at <= $2`), the same
as-of discipline the query already applies to `risks.created_at`. A brief
dated before the first link still ranks the risk.
