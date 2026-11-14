# #1809 — organisation risk categories, expand step (decisions log)

Expand step of the #1745 rollout (expand → migrate → contract). Adds the
tenant-scoped category table, seeds it, gives every risk a reference to its
own tenant's category, and dual-writes the reference and the old enum. Nothing
user-visible changes beyond a read-only list endpoint.

- detection_tier_actual: none
- detection_tier_target: none

## D1 — table name `org_risk_categories`

The table name `risk_categories` makes sqlc emit a `RiskCategory` struct,
which collides with the generated `RiskCategory` enum type that the expand
step keeps. `org_risk_categories` matches the existing `org_themes` and
`org_units` naming for tenant-owned vocabulary. The contract step can keep
the name; it reads naturally once the enum is gone.

## D2 — dual-write lives in a trigger, with the API resolving references

Risks are written from at least five paths: the API store, aggregation
meta-risks (`CreateAggregateRisk`, the rule engine), the MCP write-proposal
applier (raw SQL), the demo seed, and SQL fixtures. Repeating the dual-write
at every call site risks one path leaving the reference NULL, so the
`risks_category_sync` BEFORE trigger owns the invariant:

- enum only → the trigger sets the matching reference (seeding the tenant if
  needed);
- reference only changed → the trigger carries the enum along;
- both → they must agree, or the write is a `check_violation`.

The API store resolves the caller's slug or id against the tenant's
categories first (so an unknown value is a 400 naming the valid slugs, and an
id works), then writes both columns. `risks.category_id` is NOT NULL after
the backfill. The contract step drops the trigger together with the enum.

## D3 — one seeding function, called eagerly and lazily

`seed_risk_categories(tenant)` is idempotent (`ON CONFLICT DO NOTHING`) and
SECURITY INVOKER, so under `atlas_app` the RLS write policy still limits a
caller to its own tenant. It is called:

- by the migration, for every `tenants` row and every bare tenant id that only
  appears on `risks`;
- at tenant creation: admin tenant create, the first-user bootstrap, the
  self-host `seed.sql`, and the demo seed;
- lazily by the store (list, create) and the trigger, because many tenants
  exist only as UUIDs on rows (pre-slice-144 installs, integration tests,
  e2e fixtures). Without the lazy call those tenants would list zero
  categories and their risk writes would fail the NOT NULL reference.

## D4 — read-only list at `GET /v1/risks/categories`

Under `/v1/risks/` the existing `risks` authz resource and feature gate apply
unchanged, so no Rego or flag edits are needed for a read. The management
verbs (#1745's later tickets) may move the collection to its own resource
type when writes need risk-admin / tenant-admin permission.

## D5 — no category change on PATCH yet

`PATCH /v1/risks/{id}` accepts only `level` and `org_unit_id`, and there is no
other risk update surface. The expand step therefore adds no new way to change
a risk's category; the trigger already keeps both columns in step for any
future update path (`UPDATE ... SET category` or `SET category_id`), which the
integration test exercises directly.
