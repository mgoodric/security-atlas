# 677 — Metrics correctness pass (freshness contradiction, count labeling, green-badge-for-zero)

**Cluster:** Metrics
**Estimate:** M (1-2d)
**Type:** JUDGMENT (badge default semantics + count labeling)
**Status:** `ready` — clusters three metric-correctness findings (ATLAS-020 + 021 + 023).

## Narrative

Three related metric-display defects, re-verified on `main` build `2a3805b` in the demo
tenant. Bundled because they share the metrics surface and likely overlapping computation.

| Sub           | Finding                                                                                                                                                                                                                                                                                                           |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ATLAS-020** | Dashboard widget says "evidence freshness **100%** within window, 50/50 fresh, 0 past window"; the Metrics view says Evidence freshness = **0.0%** ("Latest 0.0%"). Two views contradict for the same tenant — a board KPI vs the dashboard hero number.                                                          |
| **ATLAS-021** | Evidence count mismatch: dashboard freshness widget is over **50** records ("0 of 50"); the Evidence ledger reports **200** ("Showing 50 of 200"). Disagree by 4×; unclear which is authoritative (likely latest-per-(control,cell) vs all append-only rows — if so it's a labeling problem, but reads as a bug). |
| **ATLAS-023** | A metric with value **0.0% / 0 and "no target set"** still renders a green **"on target"** pill ("The threshold badge defaults to green until a target is configured"). A 0% board KPI shown green is actively misleading to executives.                                                                          |

Note: the freshness side of ATLAS-020 may be partly downstream of slice 671 (the evaluator
not running on seeded evidence) — but the dashboard-vs-metric **contradiction** is a separate
computation/consistency bug (the two read freshness differently), and 021/023 are independent.

## Threat model

Read-only metrics; tenant-scoped. No data/scope/wire change. The badge-default change is a
correctness/UX decision; the freshness/count fix must use one consistent computation.

## Acceptance criteria

- [ ] **AC-1 (023).** A metric with no target configured renders a **neutral** badge (e.g.
      "no target" / muted), NOT green "on target". Green is reserved for value-meets-target.
      JUDGMENT (decisions log): the default-badge semantics.
- [ ] **AC-2 (020).** Evidence freshness reports **one consistent value** across the dashboard
      widget and the metrics view (reconcile the two computations to a single source/definition).
      Coordinate with slice 671 so freshness is computed at all in a seeded tenant.
- [ ] **AC-3 (021).** The evidence COUNT semantics are consistent + clearly labeled: if the
      dashboard counts latest-per-(control,cell) and the ledger counts all append-only rows,
      label each so they don't read as a contradiction (or reconcile to one count).
- [ ] **AC-4.** Tests pin: zero/no-target metric → neutral badge; freshness identical across
      both views for a fixture tenant; the two evidence counts are labeled/consistent.

## Anti-criteria

- Does NOT change the underlying evidence ledger or freshness window definition (display +
  consistency + badge semantics only).
- Does NOT show green for a genuinely-unmet/no-target metric.

## Dependencies

- The metrics surface (`internal/api` metrics + `web/app/(authed)/dashboards/metrics`) + the dashboard freshness widget.
- Freshness side coordinates with slice 671 (evaluation/freshness compute on seeded data).

## Notes

Source: 2026-06-10 demo-tenant audit, items **ATLAS-020 (high/major), ATLAS-021 (medium/major),
ATLAS-023 (medium/major)**. Re-tested open on `2a3805b`.
