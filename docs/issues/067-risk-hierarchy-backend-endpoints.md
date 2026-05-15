# 067 — Risk-hierarchy backend read endpoints

**Cluster:** Risk / backend
**Estimate:** 2-2.5d
**Type:** AFK

## Narrative

Surface the missing backend read endpoints that slice 056 (Hierarchical risk dashboard view) needs to fully ship. Slice 056 shipped the `/risks/hierarchy` three-panel UI — org tree, theme heatmap, decision timeline — and bound the decision-timeline panel fully to slice 055's merged endpoints. But three of its data surfaces have no backend: per-org-unit risk counts, the `themes × org_units` heatmap aggregation, and the per-cell contributing-risk list. Slice 056's decisions log (`docs/audit-log/056-hierarchical-risk-dashboard-decisions.md`) records the full gap inventory.

This slice fills the missing endpoints. It mirrors the 040→066 / 041→064 precedent: the frontend slice shipped UI shells + binding empty-state placeholders, and the backend slice fills the real endpoints behind them.

The work:

1. **`GET /v1/org_units?include_risk_counts=true`** — the `include_risk_counts` query param is currently parsed but ignored. Honor it: each org-unit node returns its aggregate risk count broken down by severity. Also: `riskWire` (slice 019's risk API shape) carries no `org_unit_id` / `themes` / severity fields — add them so the org tree and downstream panels can attribute risks without a second round-trip.
2. **`GET /v1/risks/theme-heatmap`** — a `themes × org_units` aggregation: per cell, the contributing-risk count and an aggregate severity. No such endpoint exists today — this is the heatmap panel's central data source.
3. **Per-cell contributing-risk list** — `GET /v1/risks` gains `?theme=<id>&org_unit=<id>` filters (additive, like slice 066's `?sort=`), so the heatmap-cell-click side panel can page the contributing risks.
4. **Richer `GET /v1/decisions` filters** — slice 055's list endpoint supports `?status=` + `?revisit_due_within_days=`; add `?constraints=<...>` (multi), `?decision_maker=<...>`, and a `?revisit_by_from=/?revisit_by_to=` range so slice 056's URL-deep-linkable filter bar binds server-side instead of filtering client-side.

The slice adds no new product capability and no migration — it surfaces existing data (org_units, themes, risks, decisions — all schema from slices 052/053/019/055) behind dashboard-grained read paths. It delivers value because slice 056's hierarchical risk dashboard — already merged and the CISO/program-lead surface — can bind its three placeholder panels to real data.

## Acceptance criteria

- [ ] AC-1: `GET /v1/org_units?include_risk_counts=true` — each org-unit node gains `risk_counts` — a map of severity → count for risks attributed to that org_unit. Without the param the response shape is unchanged (additive, back-compatible).
- [ ] AC-2: `riskWire` (the `GET /v1/risks` row shape) gains `org_unit_id`, `themes` (array), and `severity` — sourced from the slice-052 risk-hierarchy + slice-053 theme schema. Existing fields and filters are unchanged.
- [ ] AC-3: `GET /v1/risks/theme-heatmap` — returns a `themes × org_units` grid: per cell `{theme_id, org_unit_id, risk_count, aggregate_severity}`. Built-in themes sort before tenant-private themes (matches slice 056 AC-3's rendering contract).
- [ ] AC-4: `GET /v1/risks` accepts `?theme=<id>` and `?org_unit=<id>` filters (additive, optional, composable with existing `treatment`/`category`/`methodology` filters and slice-066's `?sort=`). Powers slice 056 AC-4's heatmap-cell-click side panel.
- [ ] AC-5: `GET /v1/decisions` accepts `?constraints=<csv>` (multi-value), `?decision_maker=<id>`, `?revisit_by_from=<iso>`, `?revisit_by_to=<iso>` — additive to slice 055's existing `?status=` + `?revisit_due_within_days=`. Powers slice 056 AC-7's deep-linkable filter bar.
- [ ] AC-6: every endpoint is tenant-scoped through the standard RLS path — slice 033 middleware is the sole tenant-context setter; no endpoint accepts `tenant_id` in query or body. Read authz reuses the existing risk/program-read role check.
- [ ] AC-7: all endpoint changes mounted via the `httpserver.go` mount-append pattern (the heatmap endpoint is a fresh path; the `/v1/risks` and `/v1/decisions` filter additions extend existing routes in place). Wire shapes match slice 056's placeholder contracts — slice 056's merged PR (gh#107) + its decisions log are the spec.
- [ ] AC-8: integration test per endpoint (≥6 tests, real Postgres): org-unit risk counts aggregate by severity · `riskWire` carries the new fields · theme-heatmap grid aggregates correctly with built-in-themes-first ordering · `/v1/risks` theme+org_unit filters compose with existing filters · `/v1/decisions` richer filters work and compose · all endpoints RLS-isolated across tenants + 403 for unauthorized roles.
- [ ] AC-9: `CHANGELOG.md` entry under `[Unreleased]/Added`.

## Follow-up (out of scope — noted, not an AC)

Re-pointing slice 056's three frontend placeholders (org-tree risk-count chips, theme-heatmap cell counts, cell-click side panel) to these endpoints is a small mechanical frontend change. Slice 056's decisions log identifies the seams. It is left as a follow-up frontend touch — this slice ships the endpoints + wire-shape contracts only, keeping 067 single-language and AFK. Slice 056's AC-2/3/4/5 flip PARTIAL → PASS once the frontend is re-pointed.

## Constitutional invariants honored

- **Invariant 4 (multidimensional scope):** `org_unit` aggregation never collapses `org_unit` and `scope_cell` — they remain distinct dimensions in the response shapes.
- **Invariant 6 (RLS):** every endpoint reads through standard tenant-scoped tables; RLS policies fire on each underlying SELECT. No new table, no `BYPASSRLS` path.
- **Slice 033 D1** (tenancy middleware is the sole tenant-context setter): no endpoint accepts `tenant_id` in query or body.
- **Invariant 9 (manual evidence is first-class):** the heatmap aggregation counts rule-driven meta-risks and manual aggregations as peers — the response carries the source distinction as a field, not as a filter that hides either.

## Canvas references

- `Plans/canvas/06-risk.md` §6.4 (risk hierarchy / org tree), §6.5 (theme taxonomy), §6.6 (aggregation rules), §6.7 (Decision Log)
- `docs/issues/056-hierarchical-risk-dashboard.md` + `docs/audit-log/056-hierarchical-risk-dashboard-decisions.md` (the frontend slice + its placeholder gap inventory)

## Dependencies

- **052** (risk hierarchy schema — `org_units`, risk-hierarchy columns)
- **053** (theme tagging — `themes` + risk-theme linkage)
- **054** (aggregation rules engine — meta-risk source distinction in the heatmap)
- **019** (risk CRUD — `riskWire` shape + `ListRisks` filters extended here)
- **055** (Decision Log CRUD — `GET /v1/decisions` filters extended here)

All dependencies merged.

## Anti-criteria (P0 — block merge)

- Does NOT bypass tenant RLS on any read.
- Does NOT fabricate risk counts or heatmap aggregates — every value resolves through an existing merged table.
- Does NOT accept `tenant_id` in query or body (slice 033 D1).
- Does NOT permit a role without risk/program-read authz to reach any endpoint.
- Does NOT break the existing `GET /v1/risks` or `GET /v1/decisions` response shapes or filters — every change is additive.
- Does NOT introduce an N+1 — the heatmap is one aggregation query; org-unit risk counts are one query joined to the tree, not one-per-node.
- Does NOT add a migration — this slice is read-only over existing schema.

## Skill mix (3–5)

- Go HTTP read handlers + `httpserver.go` mount-append
- sqlc query layer (`GROUP BY` aggregation for the heatmap; additive filter predicates)
- Postgres aggregation (`themes × org_units` grid; per-org-unit severity rollup)
- RLS-aware read endpoints + role-gated authz
- Additive API evolution (extending `riskWire`, `ListRisks`, `ListDecisions` without breaking existing callers)
