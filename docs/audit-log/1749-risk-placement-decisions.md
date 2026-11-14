# #1749 — set a risk's level and org unit — decisions log

**Issue:** https://github.com/mgoodric/security-atlas/issues/1749

## The defect (detection tier)

`POST /v1/risks` accepted neither `level` nor `org_unit_id`, and there was no
risk update endpoint. Only rule-generated and manually aggregated parents got
a level. Every directly created risk landed at `team` with no org unit, so the
tiered model (canvas §6.4) could only be populated with SQL.

- **`detection_tier_actual`: `production`.** Found during a first-run adopter
  setup on 2026-11-12.
- **`detection_tier_target`: `integration`.** An HTTP-level test that creates
  a risk under an org unit and reads its placement back catches it. Added in
  `internal/api/risks/placement_integration_test.go`.

## Decisions

### D1: an org unit fixes the level; a risk with no unit may sit at any level

A risk bound to an org unit sits at that unit's level. When the caller names
a unit and no level, the risk takes the unit's level. A named level that
differs is a 400. A risk with no org unit may take any level, because smaller
orgs may not configure org units at all (the slice 052 migration's own
rationale for a nullable `org_unit_id`).

### D2: PATCH moves placement only

`PATCH /v1/risks/{id}` accepts `level` and `org_unit_id` and refuses any other
field (`DisallowUnknownFields`), so a caller cannot mistake it for a general
update that silently drops a title change. A general risk update is separate
work. `org_unit_id: null` unbinds the risk and keeps its level. An omitted
`org_unit_id` keeps the current unit.

### D3: keeping the unit and naming no level is a no-op, not a re-check

`PATCH {"org_unit_id": <same unit>}` or a request that changes nothing writes
no audit row. A request that leaves the unit unchanged and names no level does
not re-validate the stored level against the unit. An operator can edit an
org unit's level after risks are placed under it (`PATCH /v1/org_units/{id}`),
and that edit must not make the risk unmovable. Naming a level, or naming a
unit, does validate.

### D4: the audit trail is a new append-only `risk_audit_log`

The repo keeps one audit-log table per domain (`action_plan_audit_log`,
`aggregation_rule_audit_log`, ...). `risk_audit_log` follows the
`action_plan_audit_log` shape: RLS with SELECT and INSERT policies only, plus a
trigger that denies UPDATE and DELETE for every role. It carries
`subject_module` (slice 180; `check-audit-log-subject-module` enforces it).
It has no FK to `risks`, so the record of where a risk sat outlives the risk.
The actor is the credential id, the same rule the aggregation-rules handler
uses. Wiring it into the unified audit-log endpoint (`internal/audit/unifiedlog`)
is not done here.

### D5: a move runs the aggregation hooks

The slice 054 engine counts distinct org units toward a rule's `min_teams`
threshold, so moving a risk between units can cross it. `UpdatePlacement` takes
the rules' advisory locks before the row lock and runs the after-write hook for
the risk's themes, the same order `AssignThemes` and `Delete` use (#1867,
#1880). A rule-generated meta-risk can be moved too: the engine freezes a
meta-risk's level after creation, so the move persists.

### D6: the risk wire shape gains `level`

`riskWire` had `org_unit_id` but not `level`. It now carries `level` on every
risk read and write response. Additive; the contract golden
`web/lib/contracts/risks.golden.json` is regenerated and its consumer test
passes.
