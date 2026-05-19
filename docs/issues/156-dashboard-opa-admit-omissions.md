# 156 — Dashboard read endpoints likely have the same OPA admit omission slice 148 fixed for /v1/calendar

**Cluster:** Backend / authz
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 during slice 148 implementation (`/v1/calendar` OPA
admit fix). Spillover decision D8 in
[`docs/audit-log/148-calendar-backend-decisions.md`](../audit-log/148-calendar-backend-decisions.md).

Slice 066 shipped three dashboard read endpoints —
`GET /v1/frameworks/posture`, `GET /v1/activity`, `GET /v1/upcoming` —
registered at `internal/api/httpserver.go:586-588`. Grep across both
`policies/authz/*.rego` and the embedded `internal/authz/rego_bundle/*.rego`
for `"frameworks-posture"` / `"activity"` / `"upcoming"` returns zero
matches.

Consequence (same shape as slice 148 surfaced for `/v1/calendar`): a
non-admin / non-grc role hitting the dashboard will see "Failed to load"
on the Activity / Upcoming / Posture panels because OPA returns
`allow=false` for those resource types.

The slice 066 integration tests do not catch this because they don't
wire `srv.AttachAuthz(...)` — same root cause as slice 148. The
production binary (`cmd/atlas/main.go:432`) does wire authz, so the
operator sees the symptom.

**What this slice ships:**

- Audit the slice-066 dashboard endpoints' OPA admit coverage; confirm
  the gap empirically (grep + a unit test that fails until fixed).
- Add `"frameworks-posture"` / `"activity"` / `"upcoming"` to the per-
  role readable-resources sets in `viewer.rego` / `control_owner.rego` /
  `auditor.rego` AND the embedded `rego_bundle/` mirror.
- Pin the admit at the rego layer via an OPA matrix test
  (`internal/authz/slice156_test.go`) modeled on the slice 148 test.
- Verify the resource-type extraction for `/v1/frameworks/posture`:
  `resourceFromPath` splits on `/` so the resource type is `frameworks`
  (NOT `frameworks-posture`). If `frameworks` is the type, audit
  whether the existing `defaults.rego` `catalog_resources['frameworks']`
  admit fires for this path — it might already work for read but not
  for cross-tenant safety.

## Acceptance criteria

- [ ] AC-1: Grep `policies/authz/` and `internal/authz/rego_bundle/`
      for `"upcoming"`, `"activity"`, `"frameworks-posture"`, and
      `"frameworks"`; document each endpoint's actual resource-type
      symbol in the decisions log.
- [ ] AC-2: For each endpoint that returns 403 for non-admin / non-
      grc roles, add an OPA admit per the slice 148 pattern (per-role
      readable-resources set entry, dual-mirror to source + bundle).
- [ ] AC-3: Add `internal/authz/slice156_test.go` with parametric
      tests pinning the admit set for each dashboard endpoint.
- [ ] AC-4: Verify the Activity / Upcoming / Posture panels load for
      a viewer credential on a fresh install (manual smoke via
      docker-compose).
- [ ] AC-5: CHANGELOG entry: "Dashboard read endpoints admitted for
      non-admin roles (#156; slice 066 follow-on, paired with slice
      148)".

## Dependencies

- **#066** Dashboard backend read endpoints (merged) — provides the
  three endpoints this slice gates.
- **#148** Calendar OPA admit fix (in-review) — establishes the per-
  role-readable-resources pattern this slice mirrors.
- **#035** OPA-driven RBAC (merged) — the engine that needs the new
  admits.

## Anti-criteria (P0 — block merge)

- **P0-DASH-1** Do NOT widen OPA admits beyond the slice-066 dashboard
  endpoints. Scope creep into `/v1/risks/*` or `/v1/controls/*` is
  out of scope — those have their own per-role admits already.
- **P0-DASH-2** Do NOT introduce a wildcard admit (`allow if input.action == "read"`)
  for any role. Every new admit is enumerated in the per-role
  readable-resources set, matching the slice 148 pattern.
- **P0-DASH-3** Both `policies/authz/` (source) and `internal/authz/rego_bundle/`
  (embedded) MUST be updated in lockstep. Mismatches between the two
  trees are how slice 094 originally drifted.

## Notes for the implementing agent

- The slice 148 implementation is the canonical pattern: read the
  slice 148 audit log (`docs/audit-log/148-calendar-backend-decisions.md`)
  D3 + D4 before starting.
- The slice 066 integration tests do not exercise authz. Either: (a)
  add an authz-wired smoke test as part of this slice, OR (b) accept
  that the OPA matrix test in `internal/authz/slice156_test.go` is
  the regression coverage and the integration tests stay unchanged
  (matches the slice 148 decision).
- Verify the resource-type extraction is what you expect. `resourceFromPath`
  for `/v1/frameworks/posture` returns type=`"frameworks"`, id=`"posture"` —
  so the admit lives on the `"frameworks"` set, NOT a new
  `"frameworks-posture"` set. The same logic applies to
  `/v1/activity` (type=`"activity"`) and `/v1/upcoming` (type=`"upcoming"`).

Provenance: spillover from slice 148 (parent), filed 2026-05-18 during
slice 148 implementation per CLAUDE.md Amendment 2 spillover-as-slice
convention.
