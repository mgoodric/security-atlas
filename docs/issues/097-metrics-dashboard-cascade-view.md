# 097 — Metrics dashboard + cascade-tree visualization (follow-on to slice 076)

**Cluster:** Frontend
**Estimate:** 2-3d
**Type:** JUDGMENT (UX layout calls + cascade-tree interaction patterns)
**Status:** `ready`

## Narrative

Slice 076 lands the metrics-catalog backbone — 5 tables, ~40 metrics in
YAML, 8 starter Go evaluators, the read/write API. **Slice 076 deliberately
does not ship any frontend.** This slice fills that gap.

The slice doc for 076 references "follow-on slice 078" as the dashboard
follow-on, but slice 078 already merged earlier today (2026-05-16) with
completely different scope (ESLint pin unblock). This slice is the actual
dashboard follow-on under the next-available number (097).

This slice ships:

1. A metrics-dashboard route at `/dashboards/metrics` in the Next.js frontend.
2. A board-level summary panel: each board metric's latest observation, its
   target (if set), and a trend sparkline over the last 90 days.
3. A cascade-tree explorer view: click a board metric, see its descendants
   in a navigable tree. Each node shows the latest observation + a
   green/yellow/red badge based on the target thresholds.
4. A per-metric detail page at `/dashboards/metrics/[id]` with the full
   observation series (line chart), the target configuration form, and an
   audit-trail panel of recent manual inputs.
5. A manual-input modal for `manual_input` and `external_integration`
   metrics — bound to admin role.
6. A Playwright e2e covering: navigate to dashboard, expand a board metric's
   cascade, click into a child, submit a manual input, see the new value in
   the series.

## Acceptance criteria

### Dashboard route

- [ ] AC-1: `web/app/dashboards/metrics/page.tsx` renders the board-level
      summary panel. Server Component reads `GET /v1/metrics?level=board`.
- [ ] AC-2: Each board metric card shows: name, latest observation value
      (formatted by `unit`), target (if set), and a 90-day sparkline (consumes
      `GET /v1/metrics/{id}/observations?since`).
- [ ] AC-3: Color-coded threshold badge:
  - green: target met or no target set
  - yellow: between warning_threshold and target
  - red: at or beyond critical_threshold
- [ ] AC-4: Empty-state copy when no observations exist yet ("No data yet
      — the 15-min evaluator hasn't run, or this is a manual_input metric").

### Cascade-tree explorer

- [ ] AC-5: Clicking a board metric expands its cascade tree.
- [ ] AC-6: Consumes `GET /v1/metrics/cascade?level=board&depth=3` and
      reassembles into a tree client-side.
- [ ] AC-7: Each node in the tree is clickable; clicking navigates to the
      per-metric detail page.
- [ ] AC-8: The tree handles the `X-Cascade-Truncated: true` header by
      surfacing a "depth limit reached" hint.

### Per-metric detail page

- [ ] AC-9: `web/app/dashboards/metrics/[id]/page.tsx` shows the metric
      definition, immediate parents + children, full observation series, and
      the target form.
- [ ] AC-10: Line chart shows the observation series with target +
      warning + critical horizontal lines overlaid.
- [ ] AC-11: For `manual_input` metrics, an admin-only "Submit value"
      modal opens; on submit, `POST /v1/metrics/{id}/inputs` is called and
      the series re-fetches.
- [ ] AC-12: Audit trail panel lists recent manual inputs (consumes
      `GET /v1/metrics/{id}/observations` filtered to `source LIKE 'manual:%'`).

### Tests + quality

- [ ] AC-13: Vitest unit tests for the cascade-tree reassembly logic and
      the threshold-badge color calculation.
- [ ] AC-14: Playwright e2e: navigate to /dashboards/metrics, expand
      Audit-readiness cascade, click into Per-framework-coverage, submit a
      manual input, assert the new value appears in the series.
- [ ] AC-15: shadcn/ui components used throughout (Card, Badge, Dialog,
      Form, Chart). No bespoke styling outside the shadcn theme.

### Quality

- [ ] AC-16: Decisions log at
      `docs/audit-log/097-metrics-dashboard-cascade-view-decisions.md` records
      the cascade-tree layout choice (vertical tree vs. horizontal vs. graph)
      and the chart library choice (Recharts vs. Visx vs. shadcn Chart).
- [ ] AC-17: `mkdocs build --strict` green (any new docs page).
- [ ] AC-18: Pre-commit clean. CI green.

## Constitutional invariants honored

- **#1 (One control, N framework satisfactions)** — the per-framework
  filter dropdown on the dashboard reads the existing scope graph; no
  per-framework dashboard duplication.
- **#6 (Tenant isolation)** — all observation + target API reads happen
  via the tenant-bound atlas_app pool through the BFF layer.
- **AI-assist boundary** — the dashboard renders values; it never
  auto-generates narrative interpretation.

## Anti-criteria (P0)

- **P0-A1**: Does NOT modify the slice-076 backend API (additive only —
  the dashboard consumes the existing 7 endpoints).
- **P0-A2**: Does NOT add new metrics or evaluators to the catalog. The
  catalog content is the slice-076 source of truth.
- **P0-A3**: Does NOT auto-narrate metric dips. Templated rollups in the
  board pack consume the values; narrative interpretation is human-authored.

## Dependencies

- **076** — the catalog backbone, the read/write API, and the 15-min
  evaluator cron. This slice cannot start until 076 is merged.
- **005** — the Next.js + shadcn frontend bootstrap.
- **035** — admin-role RBAC (the manual-input write is admin-gated).

## Why this slice number is 097

The slice 076 spec references "follow-on slice 078" for the dashboard
work. Slice 078 already merged on 2026-05-16 with completely different
scope (ESLint pin unblock). 097 is the next-available slice number after
the deletion-candidates follow-on (096). Documented in slice 076's
decisions log D1.
