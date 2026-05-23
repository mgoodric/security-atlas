# 256 — Coverage column in /controls/{id} coverage table (strength × effectiveness × scope predicate)

**Cluster:** Frontend + Backend / data-bound surface
**Estimate:** 1d
**Type:** JUDGMENT

## Narrative

Surfaced during slice 204's per-page audit of `/controls/{id}` (see
`docs/audit-log/204-page-audit-control.md`, Finding 4). The mockup's
coverage-by-framework table (`Plans/mockups/control.html` lines 161-257)
has **six** columns:

1. Framework requirement
2. STRM (relationship type badge)
3. Strength (numeric)
4. **Coverage** (numeric, strength × 30-day effectiveness × frameworkscope predicate)
5. Strength bar (visual rendering of the **coverage**, not the raw strength)
6. Chevron (per-row drill into mappings inspector)

The live table (`web/components/control/coverage-table.tsx`) has
**four** columns: Framework requirement · STRM · Strength · Strength bar.
**Coverage** and the chevron are missing.

The Coverage column is the page's headline data-bound metric. The
mockup's footer (line 254-256) explicitly says: "coverage is strength ×
30-day effectiveness, intersected with the framework's scope predicate.
Where the framework is out of scope, coverage is n/a (the requirement
is not evaluated for this org)." Without that column, the table is a
re-render of the mappings list — not a coverage view.

**Slice 041's stated reason for opting out** (`coverage-table.tsx`
lines 11-17): "The slice does NOT recompute strength × effectiveness
per row — that weighted number is a framework-dashboard concern (slice
008/012 territory), and fabricating it here would risk a number that
disagrees with the backend."

That reasoning is sound — UI-honesty cuts hard against client-side
computation that the backend has not blessed. The correct resolution
is to **promote Coverage to a first-class backend field** on
`GET /v1/controls/{id}/coverage` so the page renders the backend's
authoritative number. This slice does that.

## Threat model

**Verdict.** **no-mitigations-needed.** Adding a computed field to an
existing tenant-scoped read endpoint. No new auth surface, no new
mutation, no new external IO. The computation runs server-side over
data already inside the RLS boundary (strength from `scf_mappings`,
effectiveness from the eval engine's existing 30-day rollup, scope
predicate from FrameworkScope). All three inputs already gate on the
caller's tenant context per main's RLS plumbing.

## Acceptance criteria

- **AC-1 (backend).** `GET /v1/controls/{id}/coverage` adds a
  `coverage` numeric field per requirement row:
  - `strength × 30d_pass_rate` when the requirement's framework
    version is in scope for this control.
  - `null` (rendered as "n/a") when the requirement's framework
    version is out of scope (the existing `outOfScopeFvIds` set;
    same source that the live page already computes).
  - JSON shape: each row in `requirements[]` gains
    `"coverage": 0.94 | null`.
- **AC-2 (backend).** Integration test in
  `internal/api/controldetail/` covers: in-scope row returns a
  numeric `coverage`; out-of-scope row returns `null`; row with
  zero effectiveness data returns `null` (not `0` — distinguish
  "no data" from "perfectly failing").
- **AC-3 (frontend).** `web/components/control/coverage-table.tsx`
  adds a `Coverage` column (right-aligned, mono numeric, two
  decimal places). Out-of-scope rows render `n/a` in muted style.
  The strength bar visual changes: its filled portion equals the
  coverage value (0-1 clamped), not the strength value, matching
  the mockup (lines 192, 203, 214 — bars fill at coverage %, not
  strength %).
- **AC-4 (frontend).** Per-row chevron added (right-most column,
  `data-testid="coverage-row-chevron"`). Clicking the row opens
  a per-requirement deep-dive — JUDGMENT call on destination
  (see below). At minimum, the chevron is visible affordance;
  the destination can be a placeholder if necessary.
- **AC-5 (frontend).** Mockup-style footer text added below the
  table: "coverage is strength × 30-day effectiveness, intersected
  with the framework's scope predicate. Where the framework is
  out of scope, coverage is n/a." (Matches mockup lines 254-256.)
- **AC-6.** Vitest unit coverage for the strength-bar's new
  binding (filled portion = coverage when in scope, 0% when
  out of scope or null).
- **AC-7.** Playwright e2e regression: when a control has at
  least one in-scope and one out-of-scope mapped requirement,
  the table renders both with the correct Coverage values.

## Constitutional invariants honored

- **Invariant 1 (One control, N framework satisfactions).** The
  Coverage column is the visual proof of the invariant — one
  control's posture, evaluated per-framework, weighted by
  STRM strength × effectiveness × frameworkscope.
- **Invariant 5 (FrameworkScope intersects).** The `null` coverage
  for out-of-scope rows is the visual instantiation of this
  invariant: PCI requirement with no CDE → coverage is n/a, not 0.
- **Invariant 2 (ingestion / evaluation separated).** The
  coverage field is computed at evaluation time from already-
  persisted evidence; it does not mutate the ledger.
- **UI-honesty.** The page renders the backend's computed
  coverage; no client-side fabrication. Out-of-scope is `null`
  → `n/a`, not 0 — distinguishes "not applicable" from "failing".

## Canvas references

- `Plans/canvas/03-ucf.md` — UCF strength × effectiveness math
- `Plans/canvas/05-scopes.md` §5.5 — FrameworkScope intersection
- `Plans/canvas/07-metrics.md` — coverage as a KPI primitive
- `Plans/UCF_GRAPH_MODEL.md` — STRM strength semantics
- `docs/audit-log/204-page-audit-control.md` Finding 4

## Dependencies

- **#204** (UI parity audit) — parent.
- **#018** (effective-scope endpoint) — already on main; the
  scope predicate input.
- **#012** (effectiveness rollup) — already on main; the 30d
  pass-rate input.
- **#008** (coverage endpoint scaffolding) — already on main; this
  slice adds a field to its response.

## Anti-criteria (P0 — block merge)

- **P0-256-1.** Does NOT compute coverage client-side as a
  fallback. If the backend response lacks the field, render
  `—` (loading or error) — not a client-computed value.
  Slice 041's "risk of disagreement with the backend" warning
  is the constitutional reason.
- **P0-256-2.** Does NOT round to integer percent in the JSON
  response. Keep numeric precision (e.g., `0.9382`) and round
  in the renderer.
- **P0-256-3.** Does NOT change the existing Strength column
  shape (numeric, two decimals). The user must be able to see
  both numbers to understand the weighting.
- **P0-256-4.** Does NOT ship the chevron drill destination as
  a route 404 (the slice 178 dead-link anti-pattern). Either
  ship the destination as a placeholder page OR render the
  chevron visually but make the row non-clickable with a
  tooltip ("per-requirement view lands in slice NNN").

## JUDGMENT notes (for the implementing engineer)

- **D1.** What "30-day effectiveness" means when the control has
  < 30 days of evidence. Options:
  (a) prorate (use whatever rolled-up rate exists).
  (b) `null` coverage until 30 days elapse.
  Recommendation: (a) prorate with an explicit `coverage_window_days`
  field alongside `coverage` so the renderer can show "(7d window)"
  in the cell sub-line. Engineer decides; record in
  `docs/audit-log/256-coverage-column-decisions.md`.
- **D2.** Chevron drill destination. Options:
  (a) `/controls/[id]/mappings/[edge_id]` — per-edge deep-dive page
  (new route, slice-sized).
  (b) Tab-jump to the Mappings tab if #254 has landed
  (`/controls/[id]?tab=mappings&edge=<edge_id>`).
  (c) Disabled chevron with explanatory tooltip ("per-requirement
  inspector lands in slice NNN").
  Recommendation: (c) — keeps this slice small. File the
  inspector as a separate followup. Record decision.

## Skill mix (3-5)

1. Go + sqlc — adding a computed field to a coverage query
   (existing handler at `internal/api/coverage/`).
2. PostgreSQL CTE composition — `strength * 30d_pass_rate`
   intersected with FrameworkScope predicate.
3. shadcn/ui table cell + Tailwind — Coverage column rendering,
   muted "n/a" style.
4. UI-honesty discipline — the slice 041 "no client-side
   fabrication" rule informs the backend-first fix shape.
5. JUDGMENT-slice decisions log.
