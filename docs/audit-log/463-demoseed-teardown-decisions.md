# 463 — `demoseed.Seeder.Teardown` tenant-scoped framework orphan — decisions log

- detection_tier_actual: integration
- detection_tier_target: integration

The bug (Teardown leaving tenant-scoped `frameworks` + `framework_versions`
rows orphaned after a Seed → Teardown round-trip) is an integration-tier
artifact: it only manifests against a real Postgres where `Apply` actually
writes the fallback framework rows and `Teardown` then runs its DELETE sweep.
No cheaper tier can catch it — it is a DB-state round-trip invariant, not a
pure-Go branch. It was surfaced at the integration tier (slice 462's
shared-DB observation) and is reproduced + fixed + regression-pinned at the
integration tier. `actual == target == integration` — NOT a coverage-tier or
enrolment gap.

## Context

Parent slice 462 (test-harness hardening for the admindemo suite) observed
that `demoseed.Seeder.Teardown` "leaves the `tenants` row behind even on a
fully-passing baseline run" and recorded a hypothesis (462 decisions log, the
Revisit list): "the FK-order list at `seeder.go:411-442` may be missing a
child table that holds the tenant FK, blocking the final `DELETE FROM
tenants`."

Investigating that hypothesis against current `main` (all migrations applied)
disproved its literal form and located the real product gap:

1. **There are NO foreign keys into `tenants`.** A repo-wide search
   (`REFERENCES tenants` / `tenants(id)`) returns nothing — `tenant_id`
   columns are plain UUIDs, not FK-constrained to `tenants` (this is
   deliberate: tenancy is enforced at the RLS layer, canvas invariant #6, not
   via referential FKs). So the final `DELETE FROM tenants` can never be
   FK-blocked, and an empirical repro confirmed the `tenants` row IS deleted on
   current `main` (0 rows remaining, no error). The parent's `tenants`-row
   framing predated or conflated the actual leak.

2. **The actual leak is `frameworks` + `framework_versions`.** Diffing the
   set of tables `Apply` writes (24, extracted from `INSERT INTO` in
   `writers.go`/`seeder.go`) against the set `Teardown` deletes (30) found
   three writes with no matching delete: `frameworks`, `framework_versions`,
   and `super_admin_audit_log`. The framework pair is **tenant-scoped** —
   `Apply` writes them (with `tenant_id = demo tenant`) on the catalog-less
   fallback path (`writers.go` `writeAuditPeriodsAndSamples`, lines 254-269:
   when the global SCF catalog `framework_versions WHERE tenant_id IS NULL` is
   absent, it synthesizes a `Demo Framework` + version so the audit periods
   have a valid `framework_version_id` to reference). Teardown omitted both, so
   they orphaned under the torn-down tenant on every catalog-less teardown (the
   integration DB has no bundled catalog, so this fires every integration run).

An empirical probe (`TestSeedTeardown_RoundTrip`, run with the fix stashed)
confirms it: post-teardown `frameworks = 1`, `framework_versions = 1` for the
seeded tenant. With the fix: both 0.

## Decisions made

### D1 — Resolve the open question: tenant CREATE/DELETE; framework ADOPT-or-fallback-CREATE → delete-only-the-fallback

The slice's open question: does Seed CREATE the tenant (so Teardown must
DELETE it) or ADOPT a pre-existing one (so neither touches the tenant row)?
Reading the shipped code resolves it per-entity, not globally:

- **Tenant:** `Apply` CREATES it (`writeTenantRow`), refuses to run against any
  pre-existing tenant that lacks the slice-205 forensic mark. So the contract
  is create/delete, and `Teardown` correctly DELETEs it (`seeder.go:442`,
  unchanged — already correct).
- **`frameworks` / `framework_versions`:** `Apply` ADOPTS the global SCF
  catalog when present (writes nothing to these tables) and CREATES a
  tenant-scoped fallback pair only when the catalog is absent. So the inverse
  is conditional: `Teardown` must delete ONLY the tenant-scoped fallback rows
  it created, and must NOT touch the adopted global catalog.

**Chosen:** add `DELETE FROM framework_versions WHERE tenant_id = $1` then
`DELETE FROM frameworks WHERE tenant_id = $1` to the teardown sweep. The
`WHERE tenant_id = $1` predicate is exactly the create/delete contract for the
fallback rows AND the adopt-safety guard for the catalog in one expression: the
catalog rows have `tenant_id IS NULL` and can never match a non-null demo
tenant UUID, so the global catalog is untouchable by construction (not by the
seeder's care — by the predicate). When the catalog IS present, the fallback
rows were never written, so the two new deletes are 0-row no-ops.

**Rationale:** lowest-blast-radius fix that makes Teardown a true inverse of
Apply. No schema change, no CASCADE addition (rejected for the same reason
slice 462 D3 rejected it — platform-wide tenant-deletion semantics, out of
scope). It mirrors the existing teardown statements' `WHERE tenant_id = $1`
shape exactly. **Confidence: high** — proven RED→GREEN against real Postgres.

### D2 — `super_admin_audit_log` deliberately NOT added to the teardown sweep

`Apply` writes one `super_admin_audit_log` row (the `demo_seed_apply` meta
record); `Teardown` writes one (`demo_seed_teardown`). This table is
PLATFORM-GLOBAL — it has no `tenant_id` column (slice 142 / slice
`20260521030000_super_admins_full.sql` D1 records `super_admin_audit_log` as
platform-global, no `tenant_id`). Leaving these rows is correct on two
grounds: (a) a `WHERE tenant_id = $1` delete is impossible (no such column),
and a slug/action-keyed delete would be a different, broader contract; (b)
threat-model R (Repudiation) — the platform-level record that a demo seed and
teardown happened is exactly the audit trail that SHOULD survive the teardown
of the demo content. The round-trip test's `allSeededTables` excludes it with
an explicit comment so a future maintainer does not add a sweep for it.
**Confidence: high.**

### D3 — Coverage floor left at 81 (NOT lifted)

Measured merged (`gocovmerge` of unit + integration `-coverpkg=./...`)
`internal/demoseed` coverage is **83.33%** (435/522 statements) with the new
tests, vs the slice-320 baseline of 83.1% and the floor of 81. The ~0.2pp lift
is noise (the new integration tests exercise the two added DELETE statements +
walk the teardown loop more, but the loop was already largely covered). Per the
slice ratchet rule ("if coverage stays ~same, leave the floor") and the
directive for this batch, the floor stays at 81 — lifting it to 83 would set a
fragile ratchet on a sub-percent delta that the integration tier's row-count
nondeterminism could later dip below. The tests earn their keep as a regression
pin, not a coverage lift. **Confidence: high.**

## Revisit once in use

- **Catalog-present teardown path.** The fix's two new deletes are 0-row
  no-ops when the global SCF catalog is bundled (the production default once
  the catalog ships). The integration DB is catalog-less, so the test exercises
  the fallback-create → fallback-delete path. If a future test environment
  seeds the full SCF catalog before running demoseed, the round-trip test still
  passes (the framework rows are simply never written, so "back to zero" holds)
  — but the _delete-the-fallback_ arm would no longer be exercised there. The
  current integration DB keeps that arm covered; re-check if the catalog
  becomes a test-bring-up default.

- **Other writes without a matching delete.** This slice audited Apply-writes
  vs Teardown-deletes and found only the framework pair (tenant-scoped, now
  fixed) and `super_admin_audit_log` (platform-global, intentionally retained).
  If a future slice adds a new tenant-scoped table to `Apply`, the round-trip
  test (`allSeededTables`) is the guard — but the maintainer must add the new
  table to BOTH the seeder's teardown sweep AND `allSeededTables`, or the test
  will red on the orphan (which is the desired behavior). The test is now the
  enforcement surface for "Teardown is the inverse of Seed".

## Confidence summary

| Decision                                                                            | Confidence |
| ----------------------------------------------------------------------------------- | ---------- |
| D1 — per-entity contract; add tenant-scoped framework_versions + frameworks deletes | high       |
| D2 — leave super_admin_audit_log (platform-global, threat-model R)                  | high       |
| D3 — coverage floor left at 81 (0.2pp lift is noise)                                | high       |

Top of the revisit list: the catalog-present teardown path (the fallback-delete
arm is only exercised on a catalog-less DB; the integration DB currently keeps
it covered).
