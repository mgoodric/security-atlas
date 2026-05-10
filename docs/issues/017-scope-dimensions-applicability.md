# 017 — Scope dimensions + scope cell + applicability_expr engine + default single-cell seed

**Cluster:** Scope + FrameworkScope
**Estimate:** 2d
**Type:** AFK

## Narrative

Implement the multidimensional scope model. Define the default dimension set (business_unit, environment, geography, cloud_account, data_classification, product_line) as configurable per-tenant. A `scope_cell` is a tuple over these dimensions; orgs declare which cells exist. Build the `applicability_expr` engine: each control's expression is a JSON-encoded boolean over scope dimensions; the engine returns the **applicability set** (the subset of cells where this control is applied). Seed every fresh deployment with a default single-cell org so solo deployments work out of the box without forcing scope modeling on day one. The slice delivers value because controls with `applicability_expr` now evaluate over the right cells and not the wrong ones.

## Acceptance criteria

- [ ] AC-1: Tenant config defines scope dimensions; admins can add custom dimensions
- [ ] AC-2: `POST /v1/scopes/cells` creates a scope cell with a dimension tuple; validated for required dimensions
- [ ] AC-3: `applicability_expr` engine: given `{"environment IN ('prod', 'staging') AND data_classification IN ('restricted', 'confidential')"}`, returns the matching scope cells from the universe
- [ ] AC-4: Empty applicability_expr defaults to "all cells"
- [ ] AC-5: Fresh deploys seed a single default scope cell with sane defaults (`bu=default`, `env=prod`, `data_classification=internal`)
- [ ] AC-6: Slice 012's evaluation engine consumes the applicability set when computing control state
- [ ] AC-7: `GET /v1/scopes/cells` returns the universe; `GET /v1/controls/:id/applicability` returns the subset that applies to a given control

## Constitutional invariants honored

- **Invariant 4 (multidimensional scope):** scope is N-dimensional, not hierarchical
- **Invariant 6 (RLS):** scope-cell rows tenant-scoped

## Canvas references

- `Plans/canvas/02-primitives.md` §2.4 (Scope multidimensional table)
- `Plans/canvas/05-scopes.md` §5.1–5.3 (dimensions, per-cell eval, inheritance)

## Dependencies

- #002

## Anti-criteria (P0)

- Does NOT model scope as a tree
- Does NOT implement a full custom DSL grammar — JSON-encoded boolean is sufficient for v1 (per gate resolution)
- Does NOT silently drop cells that don't match dimension schema

## Skill mix (3–5)

- Go expression evaluator (JSON-encoded AST)
- Postgres set operations
- sqlc-typed queries
- JSON Schema validation for dimension declarations
- Seeding patterns
