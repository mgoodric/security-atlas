# 462 — admindemo demo-tenant leak cleanup — decisions log

- detection_tier_actual: integration
- detection_tier_target: integration

The bug (a leaked `demo` tenant false-redding `TestSeed_EnvUnsetGets503AndDoesNotSeed`
on a dirty-DB re-run) is an integration-tier artifact and was both surfaced
(slice 461 verification) and reproduced/fixed at the integration tier. No
cheaper tier could have caught it — it is a cross-test shared-DB state
interaction that only exists when the suite runs against a non-fresh Postgres,
which is the integration tier by definition. `actual == target == integration`,
so this is NOT a coverage-tier or enrolment gap.

## Context

`internal/api/admindemo`'s integration suite uses a fixed `demo`-slug tenant for
the seed/teardown happy path. Three tests (`TestSeed_EnvUnsetGets503AndDoesNotSeed`,
`TestSeed_NonAdminGets403`, `TestSeed_HappyPath`) called `cleanupDemoTenant(t, "demo")`,
which previously registered ONLY a deferred `t.Cleanup` removal. The env-unset and
non-admin tests then assert `!demoTenantExists("demo")` as a correctness invariant
(env unset / non-admin ⇒ no seed ⇒ no demo tenant). When a prior run left a `demo`
tenant behind — either because a different package `t.Fatal`'d and killed the
`go test` process before admindemo's `t.Cleanup`s ran (the spec's abort scenario),
OR because `demoseed.Seeder.Teardown` itself left the `tenants` row (observed
during this slice: even a fully-passing baseline run left a `demo` row behind) —
the next run's env-unset test saw the stale tenant and false-redded at
`handler_integration_test.go:261`, blaming the env-unset path for a row it never
created.

Parent: slice 461, which fixed the analogous SCF-seed self-cleanup class for
`scf_anchors` by making the seed guard self-correcting rather than assuming prior
state. This slice applies the same philosophy to the `tenants` table.

## Decisions made

### D1 — Fix approach: synchronous self-correcting setup + retained deferred cleanup (candidate 1, hardened)

**Options considered:**

- (1) Establish the precondition synchronously at the top of the test (delete any
  pre-existing `demo` tenant before the body) — the spec's recommended candidate.
- (2) Promote demo-tenant teardown to a package `TestMain` that wipes the slug
  before any test runs.
- (3) Use a per-test unique demo slug to avoid the fixed-`"demo"` collision.

**Chosen:** (1), hardened. `cleanupDemoTenant` now (a) calls a new
`removeDemoTenant(t, slug)` SYNCHRONOUSLY at the top — establishing a clean
precondition regardless of any prior aborted (or leaky-passing) run — AND (b)
retains the deferred `t.Cleanup` that calls the same `removeDemoTenant` on the way
out, so this run's `demo` tenant does not leak to the next run even when the test
body fails.

**Rationale:** Lowest blast radius — touches only the admindemo test harness, no
production code, no handler slug assumption. It directly mirrors slice 461's
"setup self-corrects, does not assume" property: the test no longer ASSUMES the
`demo` slug is absent, it ENSURES it. Keeping the deferred cleanup as well means
the fix is robust in both directions — clean-on-setup defends against a prior
run's leak, clean-on-teardown defends against this run leaking forward. (1) over
(2): a synchronous call co-located in each test's setup is more legible than a
package-global `TestMain` side effect and keeps the cleanup logic in one place. (1)
over (3): a per-test unique slug would diverge the test from the handler's
hard-coded `demoTenantSlug = "demo"` (P0-278-2: the UI button path uses a fixed
slug), so the happy-path test would no longer exercise the real production slug —
a fidelity loss for no extra robustness over (1). **Confidence: high.**

### D2 — Removal is best-effort `Seeder.Teardown` THEN an unconditional raw FK-ordered sweep

`removeDemoTenant` first tries `demoseed.Seeder.Teardown` (the clean path for a
fully-seeded, forensically-marked leftover, which also removes its primitive child
rows), then UNCONDITIONALLY runs a raw child-row sweep + `DELETE FROM tenants`
keyed on the tenant id.

**Rationale:** `Seeder.Teardown` deliberately REFUSES to delete a tenant that does
not carry the slice-205 forensic mark (`seeder.go:392-394`) — so a bare leftover
from an abort (a `tenants` row with no seeded children) makes Teardown return an
error, which we swallow. The raw sweep is the catch-all for exactly that case, and
for the observed case where Teardown ran but the `tenants` row survived. The sweep
deletes a minimal child set (`me_audit_log`, `super_admin_audit_log`, `user_roles`,
`local_credentials`, `sessions`, `users`) in FK order before the parent — these are
the tables a bare/partial admindemo leftover can hold (seeded primitive rows like
controls/risks are removed by the preceding Teardown when present). All statements
are best-effort (`_, _ = Exec(...)`): a clean DB makes every statement a 0-row
no-op. **Confidence: high** — proven: a bare leftover (insert `demo` with no
children) is removed and the suite goes RED→GREEN with the fix.

### D3 — No CASCADE schema change; no randomized slug

Considered adding `ON DELETE CASCADE` to the tenant FKs (so a single
`DELETE FROM tenants` would cascade) — rejected: that is a production schema change
with platform-wide tenant-deletion semantics implications, far out of scope for a
test-harness robustness fix, and would need its own slice + threat-model review. The
raw FK-ordered sweep achieves the same in-test cleanup without touching schema.
Randomizing the slug (D1 option 3) was rejected per D1. **Confidence: high.**

## Revisit once in use

- **Underlying `Seeder.Teardown` tenant-row leak.** This slice fixes the TEST
  suite's robustness, but it also surfaced that `demoseed.Seeder.Teardown` can leave
  the `tenants` row behind even on a passing run (the `demo` count stayed at 1
  between consecutive green runs). The test now self-corrects around it, but the
  PRODUCT teardown leaving a row is worth a focused look — if the HTTP
  `/v1/admin/demo/teardown` path exhibits the same leak, an operator who clicks
  teardown could see a residual `demo` tenant. If confirmed as a product bug,
  file a slice against `internal/demoseed` (the FK-order list at `seeder.go:411-442`
  may be missing a child table that holds the tenant FK, blocking the final
  `DELETE FROM tenants`). NOT fixed here — out of scope (test-harness slice).
- **Child-table list drift in `removeDemoTenant`.** The raw sweep enumerates the
  child tables a bare admindemo leftover can hold. If a future slice adds a new
  tenant-scoped table that an admindemo test can populate before an abort, the
  sweep would need that table added (else the final `DELETE FROM tenants` could be
  FK-blocked again). Low risk for the env-unset/non-admin paths (they never seed),
  but re-check if admindemo's fixtures grow.

## Confidence summary

| Decision                                                           | Confidence |
| ------------------------------------------------------------------ | ---------- |
| D1 — synchronous self-correcting setup + retained deferred cleanup | high       |
| D2 — best-effort Teardown then unconditional raw FK-ordered sweep  | high       |
| D3 — no CASCADE schema change, no randomized slug                  | high       |

Top of the revisit list: the underlying `Seeder.Teardown` tenant-row leak (a
potential PRODUCT teardown bug this test slice incidentally surfaced).
