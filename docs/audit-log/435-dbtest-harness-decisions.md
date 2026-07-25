# Slice 435 — shared integration-test DB/tenant harness (`internal/dbtest`) — JUDGMENT decisions log

**Slice:** `docs/issues/435-dbtest-shared-integration-harness.md`
**Type:** JUDGMENT (constructor-naming shape, migrated-subset selection, RLS-fidelity test design)
**Date:** 2026-06-12

This slice extracts the integration-test pool/tenant/context boilerplate that
~80 suites under `internal/` each re-derive into a shared `internal/dbtest`
package, then migrates a representative subset to it. It is the highest-risk
slice in the queue: the integration tier is the project's primary evidence that
RLS enforces tenant isolation (CLAUDE.md invariant #6), so a harness that
silently weakens the role model or the RLS context turns a green suite into a
false-assurance suite. It modifies **no** production (`!_test.go`) code.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

No product bug surfaced. The bugs that surfaced were test-scaffolding
compile/wiring errors during the migration (e.g. the `risk` package has four
integration test files sharing package-level helpers; removing the shared
`openPool`/`freshTenant`/`ctxFor` defs from one file broke the other three
until they were migrated too; a stale `time` / missing `dbtest` import). All
were caught at the integration tier — `go vet -tags=integration` + the live
`go test -tags=integration -p 1` run against a real Postgres — exactly where a
test-harness refactor's errors should be caught, before the PR opened. The
AC-7 RLS-fidelity guard is itself an integration-tier test.

## D1 — Constructor naming: two clearly-named constructors, not `NewTestPool(role)`

**Decision.** Ship **two** constructors — `NewAppPool(t)` (the `atlas_app`
RLS-enforcing default) and `NewMigratePool(t)` (the privileged BYPASSRLS pool)
— rather than a single `NewTestPool(role)` that takes the role as a parameter.

**Why.** This is the load-bearing Elevation-of-privilege guard (AC-3 /
P0-435-1). With two separately-named constructors, privilege is opt-in and
spelled out at the call site: a test that wants RLS enforced types `NewAppPool`;
a test that wants BYPASSRLS cleanup types `NewMigratePool`. There is no single
seam that could _default_ to — or be fat-fingered into — the privileged pool.
A `NewTestPool(role)` signature, by contrast, has a default-value failure mode
(what does `NewTestPool("")` return?) and reads identically at the RLS-assertion
call site and the cleanup call site, so a reviewer cannot tell at a glance
whether an assertion is running through the enforcing role. The slice doc's
"Notes for the implementing agent" asked for this choice to be recorded and to
"pattern-match to whichever reads clearest at the migrated call sites" — at the
migrated `risk` / `scope` / `freshnessdrift` sites the two-constructor form
reads unambiguously (`migrate := dbtest.NewMigratePool(t)` / `app :=
dbtest.NewAppPool(t)`), which is exactly the two-pool shape the canonical
`internal/db/integration_test.go` `TestMain` already uses.

## D2 — `SeedTenant` takes the cleanup table list; it does not insert a tenant row

**Decision.** `SeedTenant(t, migrate, cleanupTables...)` returns a fresh tenant
UUID and registers a `t.Cleanup` that `DELETE`s `WHERE tenant_id = $1` from the
caller-supplied tables (FK-safe order, children first), through the **migrate**
pool. It does **not** insert a row into a `tenants` table.

**Why.** (a) There is no standalone `tenants` table in this schema that
RLS-scoped fixtures key off — a tenant is a free UUID that rows carry in their
`tenant_id` column and the `app.current_tenant` GUC selects on. This matches the
inline `freshTenant()` idiom every migrated suite used. (b) The cleanup table
set is genuinely per-suite (scope cleans `scope_cells`/`scope_dimensions`; risk
cleans `risk_control_links`; freshnessdrift cleans `control_drift_snapshots`),
so passing the tables in preserves each suite's _exact_ prior cleanup behavior —
a refactor with zero behavior change — rather than baking one table list into
the helper. (c) The migrate pool is required for cleanup because some tables are
append-only under RLS for the app role (`evidence_records`,
`control_drift_snapshots` — slice 013); passing the migrate pool to `SeedTenant`
is the explicit, named privilege escalation, never implicit.

## D3 — `WithTenantCtx` returns a tenant-tagged `context.Context`, mirroring `tenancy.WithTenant`

**Decision.** `WithTenantCtx(t, tenant)` wraps `tenancy.WithTenant` (the same
path production uses) and returns the tagged context; the `dbx` `Store` (or an
explicit `tenancy.ApplyTenant` inside a tx) applies the `app.current_tenant` GUC
per transaction. It does **not** itself open a transaction or call
`set_config` — that would diverge from the two distinct call shapes the suites
use (a `dbx`-Store path that applies the GUC internally, and a manual
`tx.Begin` + `ApplyTenant` path).

**Why.** Semantics must be _identical_ to the inline `ctx, _ :=
tenancy.WithTenant(...)` pattern it replaces (AC-2, the Information-disclosure
guard). The companion RLS GUC the canonical suite sets is **only**
`app.current_tenant` (verified by reading `internal/tenancy/apply.go` +
`context.go` — there is no second companion GUC), so `WithTenantCtx` carries
exactly that and nothing more. AC-2's behavioral test (`TestWithTenantCtx_SetsGUC`)
confirms the GUC is observably set; AC-7 confirms the resulting RLS denial holds.

## D4 — Migrated subset: `scope`, `risk`, `freshnessdrift`

**Decision.** Migrate `internal/scope`, `internal/risk` (all four of its
integration test files), and `internal/freshnessdrift` — the representative
subset spanning the three real call shapes — and defer the remaining ~80 suites
to a follow-on drain.

**Why.** The three were chosen because together they exercise every call shape
the harness must cover: (a) **RLS-bound read** — scope's `ListCells`, risk's
`Store.List`/`Get` through `dbx`; (b) **tenant-seed** — all three seed a fresh
tenant + fixtures; (c) **append-only-cleanup needing the migrate pool** —
scope cleans `evidence_records`, freshnessdrift cleans the append-only
`control_drift_snapshots` ledger AND uses the migrate pool to enumerate tenants
in its scheduler sweep. Scope additionally carries a cross-tenant negative test
(`TestRLS_OtherTenantCannotSeeCells`) that the migration must not weaken. All
three shared the _identical_ copy-pasted `appDSN`/`adminDSN`/`openPool`/
`freshTenant` block, so they are the cleanest proof the helper covers the real
idiom. Big-bang migration of all ~80 is explicitly forbidden by the slice's
anti-criteria; the remainder is filed as spillover **slice 742** (drain),
mirroring the slice-390 / 402-408 integration-enrolment drain.

`internal/risk/slice053_integration_test.go` was **left unmigrated** on purpose:
it defines its own differently-named, self-contained helpers
(`slice053OpenPool`, `slice053FreshTenant`, …) that do not depend on the shared
package-level symbols, so removing the shared helpers did not break it. It is a
lower-priority candidate for the slice-742 drain.

## D5 — AC-7 RLS-fidelity guard + the deliberate-weakening sanity check (the whole point of the slice)

**Decision.** `internal/dbtest/dbtest_test.go` carries the AC-7 negative test
`TestRLS_CrossTenantRead_DeniedThroughAppPool`: stand up two tenants, write a
control under tenant A through a `NewAppPool` pool, switch context to tenant B
via `WithTenantCtx`, and assert the read returns **zero** rows. Plus three
supporting guards: `TestWithTenantCtx_SetsGUC` (AC-2), `TestAppPool_CannotBypassRLS`
(app pool with no GUC sees zero — proves it is RLS-enforcing), and
`TestMigratePool_BypassesRLS` (migrate pool with no GUC sees the row — proves
the two constructors return different roles).

**Deliberate-weakening sanity check (REQUIRED by the slice; result recorded):**
I weakened `WithTenantCtx` to ignore the requested tenant and always return a
single process-global first-seen tenant, so the tenant-B read reused tenant A's
context. The AC-7 test then **FAILED** as required:

```
dbtest_test.go:110: tenant B saw 1 rows for tenant A's control through a dbtest app pool; RLS bypassed
--- FAIL: TestRLS_CrossTenantRead_DeniedThroughAppPool
```

Restoring the correct helper, the test **PASSED** again. The test can be made to
fail by weakening the helper, so it is testing the right thing — it is not a
tautological green. This was run against a live Postgres (a local
`postgres:16-alpine` on port 5433 with the `atlas_app` / `atlas_migrate` roles
bootstrapped from `migrations/bootstrap/01-roles.sql` and all 114 migrations
applied), not merely compiled.

## D6 — Enrolment: Leg A

**Decision.** `internal/dbtest` ships `integration`-tagged tests, so it is
enrolled in `scripts/integration-shards.txt` in **Leg A** (the serial,
shared-global-catalog leg), immediately after `internal/db`.

**Why.** It is foundational test infrastructure that sits with `internal/db` and
`internal/backup` in the serial leg; its suite is tiny (four guards) and seeds
only its own per-test tenants (no shared global-catalog dependency, so it is
safe anywhere, but Leg A is the natural home next to the canonical role-model
suite it mirrors). `just audit-integration-enrolment` and
`check-integration-shard-coverage` both pass after enrolment (114 tagged ==
enrolled, disjoint, Phase-A catalog-seed pin holds). A `KNOWN_UNENROLLED` waiver
was **not** used — that ratchet is drained-empty (slice 408) and adding to it is
a code smell; enrolling the package is correct.

## Acceptance criteria — disposition

| AC    | Status | Evidence                                                                                            |
| ----- | ------ | --------------------------------------------------------------------------------------------------- |
| AC-1  | met    | `NewAppPool` / `NewMigratePool` / `SeedTenant` / `WithTenantCtx`, each `t`-scoped with `t.Cleanup`. |
| AC-2  | met    | `TestWithTenantCtx_SetsGUC` (behavioral GUC check).                                                 |
| AC-3  | met    | Two separately-named constructors; `NewAppPool` is `atlas_app` (D1); `TestAppPool_CannotBypassRLS`. |
| AC-4  | met    | `scope`, `risk` (4 files), `freshnessdrift` migrated; inline boilerplate removed.                   |
| AC-5  | met    | `go test -tags=integration -p 1` green for all migrated pkgs + `internal/db` against live DB.       |
| AC-6  | met    | `internal/dbtest/README.md` documents the convention.                                               |
| AC-7  | met    | `TestRLS_CrossTenantRead_DeniedThroughAppPool` + deliberate-weakening check (D5).                   |
| AC-8  | met    | Enrolled in `scripts/integration-shards.txt` Leg A; both enrolment checks pass (D6).                |
| AC-9  | met    | No `!_test.go` file modified (git diff is `_test.go` + docs + shards + CHANGELOG only).             |
| AC-10 | met    | No parallel pool introduced; `-p 1` + no-retry unchanged.                                           |

## Spillover

- **Slice 742** — migrate the remaining ~80 integration suites to `dbtest`
  (drain, batched). Filed `ready`; depends on 435.
