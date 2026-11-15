# #1819 — drop the risk_category enum: decisions log

Contract step of #1745 (expand -> migrate -> contract). Migration
`20261125000000_drop_risk_category_enum` drops `risks.category`, the
`risks_category_sync` dual-write trigger and the `risk_category` type.
`risks.category_id`, the reference to the tenant's own category, is the only
record of a risk's category from here. The calls below were made at build
time and are recorded for revisit.

- detection_tier_actual: none
- detection_tier_target: none

## D1. The drop refuses to run over a disagreeing risk

Before dropping anything the migration takes an `ACCESS EXCLUSIVE` lock on
`risks` and counts the risks whose reference is null, points at no category,
or points at a category whose slug is not the enum value. Any such risk
raises `check_violation` naming the count and the first id, and the
transaction rolls back with the column still there. The expand step's
trigger should make that state impossible, but a live install must never
lose a category silently, so the migration checks instead of trusting it.
It runs as `atlas_migrate` (BYPASSRLS), so the count sees every tenant. It
also checks that: under a role RLS binds, the count would see no rows and
pass vacuously, so the migration refuses (`insufficient_privilege`) unless
the role is a superuser or BYPASSRLS. The data-safety review asked for this.

`TestDropRiskCategoryEnum_RefusesADisagreeingRisk` observes the guard both
ways in one rolled-back transaction: the migration succeeds over agreeing
rows, then fails over one risk made to disagree (with the sync trigger
disabled for that one write).

## D2. The down migration is not lossless

The enum holds only the seven built-in slugs. Once the enum is gone a
tenant can put risks on its own categories, which the enum cannot name. The
down step recreates the type, the column and the trigger, and backfills the
column from each risk's category slug. A risk on any other category gets
the placeholder `operational`, and its `category_id` is left alone, so the
reference stays correct and only the restored enum value is wrong for those
risks. After a down, those rows disagree with their enum, so the up step's
guard (D1) refuses to run again until they are fixed. That is deliberate:
re-running up over them would be the silent loss D1 exists to prevent.

The repo's ledger is forward-only (`just migrate-up`, no `migrate-down`), so
the down file is for tests and for a hand-run recovery.

## D3. `risk_category_id(tenant, slug)` replaces the trigger's slug lookup

The dropped trigger let a SQL writer name a category by slug alone, and
seeded a bare-UUID tenant's categories on the way. The demo seed and about
forty integration-test fixtures relied on that. Rather than spell out a
seed call plus a subquery at each one, the migration adds
`risk_category_id(tenant, slug)`: it seeds the tenant if needed and returns
the category id, or raises `foreign_key_violation` for an unknown slug. It
is `SECURITY INVOKER`, so under `atlas_app` RLS still limits it to the
caller's own tenant. Go writers do not use it; they resolve the category
through `risk.ResolveCategoryTx` as before.

## D4. Custom categories can now hold risks and be the default

Two refusals existed only because the enum could not hold a custom slug:
creating or moving a risk onto a category added through the management API
(`InvalidCategoryError` "cannot hold risks until the risk_category enum is
retired"), and making such a category the default. Both are removed. The
aggregation engine's meta-risk fallback now uses the default category's id
directly, so a custom default works there too. The integration tests that
asserted those 400s now assert the success path.

## D5. `UpdateRisk` no longer writes the category

`UpdateRisk` used to rewrite the enum on every full update. The category now
moves only through `UpdateRiskCategory`, which the PATCH path audit-logs
(#1812). No caller changed the category through `UpdateRisk`.

## D6. Old slugs keep working through the API

Clients that send an old enum value (`operational`, `privacy`, ...) on
`POST /v1/risks`, `PATCH /v1/risks/{id}` or the MCP `create_risk` proposal
keep working: those values are the seeded slugs, and the API resolves a
category by slug, id or display name. `TestRiskCategories_OldEnumValuesStillWork`
and the MCP category tests cover it against the post-contract schema.
