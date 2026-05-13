# 052 — Schema + migrations for risk hierarchy + themes + Decision Log

**Cluster:** Risk register
**Estimate:** 2d
**Type:** AFK

## Narrative

Extend the schema for the multi-level risk model and the Decision Log primitive described in `Plans/canvas/06-risk.md §6.4–6.7`. No business logic in this slice — just the data shape, migrations, and RLS plumbing that subsequent slices (052 theme tagging, 053 aggregation rules, 054 decision log CRUD) build on.

Three new shapes land:

1. **Risk hierarchy fields** added to `risks` — `level` (enum: `team` / `org` / `company`), `org_unit_id` (fk), plus a join table for parent/child risk aggregation.
2. **Theme taxonomy** — flat enum extensible per-tenant via `org_themes` table; `risks.themes` becomes `text[]` validated against the union of default + tenant themes.
3. **Decision Log tables** — `decisions` + four M:N link tables (`decision_risks`, `decision_controls`, `decision_exceptions`, `decision_scope_predicates`).

Idempotent + reversible migrations. RLS on every new table. No API or evaluation logic yet.

## Acceptance criteria

- [ ] AC-1: `risks` table gains `level` (enum `team` | `org` | `company`, NOT NULL default `team`), `org_unit_id` (fk to `org_units`, nullable), and `themes` (`text[]` NOT NULL default `'{}'`).
- [ ] AC-2: New `org_units` table: `id` (uuid pk), `tenant_id`, `name`, `parent_id` (self-ref, nullable), `level` (enum), `acceptance_authorities` (jsonb), timestamps.
- [ ] AC-3: New `risk_aggregations` join table: `parent_risk_id`, `child_risk_id`, `rule_id` (nullable — null = manual), `created_at`, unique on `(parent_risk_id, child_risk_id)`.
- [ ] AC-4: New `org_themes` table for tenant-private themes: `tenant_id`, `theme_name`, `description`, timestamps. Default themes (10 listed in canvas §6.5) seeded as built-ins.
- [ ] AC-5: New `decisions` table per canvas §6.7 schema: `decision_id` (text, unique within tenant), `title`, `narrative`, `constraints` (`text[]`), `tradeoffs`, `decision_maker`, `decided_at`, `revisit_by` (nullable date), `status` enum, `superseded_by` (self-ref nullable).
- [ ] AC-6: Four decision-link tables: `decision_risks`, `decision_controls`, `decision_exceptions`, `decision_scope_predicates`. Each has `(decision_id, target_id)` unique key.
- [ ] AC-7: RLS policies on every new table: tenant_id row filter; `risks` and `decisions` further restrict write to roles with the relevant authority per `org_units.acceptance_authorities`.
- [ ] AC-8: Migration runs idempotently (re-running is a no-op) and is reversible (`atlas migrate down` cleanly removes all new structures).
- [ ] AC-9: Integration tests: CRUD on each new table through the tenant-scoped Postgres connection passes; cross-tenant read of any new row fails with no rows returned (RLS verification).
- [ ] AC-10: Default theme set seeded in a separate idempotent seed migration; tenant-private themes do not collide with defaults.

## Constitutional invariants honored

- **Invariant 6** (tenant isolation via Postgres RLS) — every new table gets RLS from day one
- **Invariant 4** (scope is multidimensional, not tree) — risk hierarchy is its own dimension on Risk; org_unit is a separate axis from scope cells
- **Invariant 9** (manual evidence is first-class) — manual aggregation is a peer of automatic aggregation; both go through the same `risk_aggregations` table

## Canvas references

- `Plans/canvas/06-risk.md §6.4` — Risk hierarchy and tiered acceptance
- `Plans/canvas/06-risk.md §6.5` — Theme taxonomy
- `Plans/canvas/06-risk.md §6.6` — Aggregation rules (schema only; engine in slice 054)
- `Plans/canvas/06-risk.md §6.7` — Decision Log

## Dependencies

- **002** (schema + migrations spine) — needs the base `risks` table and migration tooling

## Anti-criteria (P0)

- Do NOT implement aggregation rule evaluation in this slice (that's 053). Schema only.
- Do NOT implement Decision Log CRUD endpoints (that's 054). Tables only.
- Do NOT collapse the four decision-link tables into one polymorphic table — explicit FKs per target type are required for RLS + auditor clarity.
- Do NOT introduce hierarchy in themes (canvas §6.5 explicitly rejects this).
- Do NOT permit aggregation rules to auto-close parent risks when children close (canvas §6.6 explicit).

## Skill mix (3–5)

- `engineering-advanced-skills:database-designer` (schema design + migration idempotency/reversibility)
- `engineering-advanced-skills:database-schema-designer` (ERD for the new tables; RLS policy review)
- `tdd` (integration tests through public CRUD interface)
- `security-review` (RLS coverage on all new tables; cross-tenant denial test)
- `engineering-advanced-skills:sql-database-assistant` (Atlas migration HCL + recursive query patterns for hierarchy walks)
