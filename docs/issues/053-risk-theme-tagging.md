# 053 — Risk theme tagging + manual aggregation API

**Cluster:** Risk register
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Build the API surface for tagging risks with themes (canvas §6.5) and for manually rolling risks up into parent-level aggregations (canvas §6.6, manual path). This slice covers the **manual** rollup pattern: a human explicitly groups child risks under a parent. The **automatic** rule engine is slice 054.

Three endpoint groups:

1. **Theme management** — assign/remove themes on a risk; list available themes (defaults + tenant-private).
2. **Manual aggregation** — create a parent risk linked to N children with a chosen severity function (`max`, `weighted_max`, `sum`).
3. **Org-unit management** — CRUD for `org_units` (used to bind risks to a hierarchy level).

The severity rollup is the meaningful piece. Default function is `max` (conservative). Tenant chooses on a per-aggregation basis.

## Acceptance criteria

- [ ] AC-1: `POST /v1/risks/{id}/themes` accepts `{themes: [string]}`; rejects themes not in the default + tenant-private set; returns 200 with updated risk.
- [ ] AC-2: `DELETE /v1/risks/{id}/themes/{theme}` removes a theme; returns 204; idempotent (re-deleting is a no-op, not an error).
- [ ] AC-3: `GET /v1/themes` returns the full theme catalog: 10 default + any tenant-private. Sorted alphabetically.
- [ ] AC-4: `POST /v1/org_units` / `GET` / `PATCH` / `DELETE` — full CRUD; `parent_id` validated against same tenant; cycle detection rejects circular parent chains.
- [ ] AC-5: `POST /v1/risks/aggregate` accepts `{parent: {title, level, org_unit_id, severity_function}, child_risk_ids: [uuid]}` — creates parent risk, populates `risk_aggregations` rows linking each child, computes initial severity via the chosen function. Returns the parent risk with its `linked_children` field populated.
- [ ] AC-6: Severity functions implemented and unit-tested:
    - `max` — straightforward
    - `weighted_max` — `max × (1 + log10(child_count))`, capped at scale max
    - `sum` — sum of child severities, capped at scale max
- [ ] AC-7: Re-aggregating with the same `(parent_title, child_set)` does not duplicate; idempotency key derived from sorted child UUIDs.
- [ ] AC-8: Closing a child risk does NOT auto-close the parent. The parent's severity recomputes (drops a contributor) but stays open until explicitly closed.
- [ ] AC-9: Integration test: tag 3 risks across 2 org_units with `ownership` theme, manually aggregate into a `org`-level parent risk, verify parent severity matches the chosen function, close one child and verify parent severity drops but parent stays `open`.
- [ ] AC-10: Cross-tenant denial: a manual aggregation request including a child_risk_id from another tenant returns 404 (not 403 — don't leak existence).

## Constitutional invariants honored

- **Invariant 6** (tenant isolation) — every endpoint goes through tenant-scoped Postgres connection; RLS enforces the boundary in storage too
- **Invariant 9** (manual is first-class) — manual aggregation is a peer of rule-driven aggregation, not a fallback

## Canvas references

- `Plans/canvas/06-risk.md §6.4` — Risk hierarchy (manual rollup path)
- `Plans/canvas/06-risk.md §6.5` — Theme taxonomy + validation
- `Plans/canvas/06-risk.md §6.6` — Severity functions

## Dependencies

- **052** (risk hierarchy + themes + decision log schema)
- **019** (risk register CRUD — base `risks` table + endpoints)

## Anti-criteria (P0)

- Do NOT implement automatic rule-driven aggregation (slice 054).
- Do NOT allow themes outside the default + tenant-private taxonomy — silently accepting unknown themes invites taxonomy drift.
- Do NOT introduce theme hierarchy (canvas §6.5 explicit rejection).
- Do NOT auto-close parent risks when children close (canvas §6.6 explicit).
- Do NOT permit cross-tenant `child_risk_id` references — returns 404 (existence-leak prevention).

## Skill mix (3–5)

- `tdd` (integration tests for each endpoint; severity-function unit tests)
- `engineering-advanced-skills:api-design-reviewer` (REST endpoint shapes, idempotency keys, error semantics)
- `engineering-advanced-skills:api-test-suite-builder` (CRUD + aggregation integration tests)
- `security-review` (cross-tenant denial; theme injection prevention; cycle detection on org_units)
- `engineering-advanced-skills:sql-database-assistant` (parent severity recompute query on child close)
