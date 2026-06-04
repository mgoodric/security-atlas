# 462 — admindemo integration suite leaks a `demo` tenant when an earlier test in a shared DB aborts

**Cluster:** Infra / Testing
**Estimate:** S (0.25d)
**Type:** JUDGMENT

**Status:** `ready`

> Surfaced during slice 461 (integration seed-order coupling). Out of scope
> for that slice — it is a DISTINCT failure class. Filed for follow-up.
> Parent: slice 461.

## Narrative

While verifying slice 461's order-independence fix by running
`go test -tags=integration -p 1 ./internal/api/...` repeatedly against the
SAME (non-fresh) Postgres, the `internal/api/admindemo` suite intermittently
failed:

```
handler_integration_test.go:261: demo tenant exists after 503; should not have been created
--- FAIL: TestSeed_EnvUnsetGets503AndDoesNotSeed
```

The failure does NOT reproduce on a fresh DB — `admindemo` passes cleanly in
isolation and in slice 461's authoritative pristine-DB full-wildcard run
(exit 0). It only appears on a **re-run against a DB already dirtied by a
prior run that aborted mid-suite**. The mechanism:

1. `admindemo`'s seed tests create a `demo`-slug tenant and rely on
   `cleanupDemoTenant(t, "demo")`, which registers a `t.Cleanup` to remove it
   AFTER the test.
2. `TestSeed_EnvUnsetGets503AndDoesNotSeed` asserts `!demoTenantExists("demo")`
   as a precondition-style invariant (env unset ⇒ no seed ⇒ no demo tenant).
3. If a PRIOR run of the suite aborted (e.g. another package `t.Fatal`'d and
   killed the `go test` process before `admindemo`'s `t.Cleanup`s ran), a
   `demo` tenant is left behind. The next run's `TestSeed_EnvUnset…` then sees
   the stale `demo` tenant and fails — a false red attributing a
   left-behind-row to the env-unset path it is testing.

CI never hits this because each CI run gets a fresh database, and the curated
package order does not abort before `admindemo`'s cleanups. The fragility is
purely a **shared-dirty-DB re-run artifact**, the same general class as slice
461 but a different table (`tenants`) and a different mechanism (deferred
cleanup skipped on abort, vs. lazy-seed guard mis-skip).

## Why this is worth fixing

- **False-red tax on local iteration.** A contributor re-running the suite
  locally without a fresh DB between runs (a natural thing during a debug
  loop) sees `admindemo` fail with a message that blames the env-unset path,
  not a stale row. They lose time reverse-engineering it (as happened during
  slice 461 verification).
- **Same hardening principle as slice 461.** The integration suite should be
  robust to a dirty starting DB, not just a pristine one. `admindemo` should
  establish its own clean precondition rather than ASSUME the `demo` tenant
  is absent.

## Candidate fixes (pick one in the JUDGMENT call)

1. **Make `TestSeed_EnvUnset…` establish its precondition.** Call
   `cleanupDemoTenant`'s removal logic SYNCHRONOUSLY at the top of the test
   (delete any pre-existing `demo` tenant before the body), not only as a
   deferred `t.Cleanup`. Lowest blast radius; mirrors the slice 461
   "self-correcting setup" principle.
2. **Promote demo-tenant teardown to package `TestMain`.** A `TestMain` that
   removes the `demo` tenant before the package's tests run guarantees a clean
   start regardless of prior abort.
3. **Use a per-test unique demo slug.** Avoid the fixed `"demo"` slug
   collision entirely. Larger change; touches the handler's slug assumption.

Recommendation: (1) — it is the minimal change and matches the
"setup self-corrects, does not assume" property slice 461 established.

## Acceptance criteria

- [ ] **AC-1.** `go test -tags=integration -p 1 ./internal/api/admindemo/...`
      is green when run against a DB that already contains a `demo`-slug
      tenant (the dirty-DB re-run case).
- [ ] **AC-2.** The fix does not weaken the assertion's intent — env-unset
      must still 503, must still not SEED a demo tenant; the fix only ensures
      the precondition is clean.
- [ ] **AC-3.** CI's curated `tests-integration` run still passes unchanged.

## Notes

- Reproduce: run `go test -tags=integration -p 1 ./internal/api/...` against a
  DB, kill it mid-suite (or let a package fail), then re-run; observe
  `admindemo`'s `TestSeed_EnvUnsetGets503AndDoesNotSeed` fail with
  "demo tenant exists after 503". `DELETE FROM tenants WHERE slug='demo'`
  then re-running → green, isolating the cause to leftover `demo` tenant
  state, not any product change.
- Parent: slice 461 (`docs/issues/461-integration-suite-seed-order-coupling.md`).
