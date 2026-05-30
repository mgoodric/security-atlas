# Slice 405 — integration-enrolment drain batch 5 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Batch 5 of the slice-390 drain. Enrols the five packages that carry a
`//go:build integration` tag but were absent from the `tests-integration`
job's package list in `.github/workflows/ci.yml` (catalogued by the slice-345
guard's `KNOWN_UNENROLLED` allowlist). Mirrors the slice-401/402/403/404 method
(depended on 404 landing first so the batches stay sequential and CI stays
green between them).

## Scope

```
internal/api/questionnaires
internal/api/ucfcoverage
internal/api/emptyset
internal/api/freshnessdrift
internal/audit/notes
```

`internal/audit/notes` is under `internal/audit/`, not `internal/api/`; enrolled
as `./internal/audit/notes/...`.

## Method

- Stood up a local harness mirroring the CI integration job: fresh
  `postgres:16-alpine` container (`security-atlas-pg-405`, port 55405),
  `migrations/bootstrap/01-roles.sql`, all 66 forward migrations applied in
  order, `atlas_app`/`atlas_migrate` passwords set. None of these five packages
  touch MinIO/NATS directly — they are DB-backed handler / audit suites needing
  only `DATABASE_URL_APP` (atlas_app role) + `DATABASE_URL` (atlas_migrate /
  BYPASSRLS role) — so no object store / queue was required for the per-package
  runs.
- Ran `go test -tags=integration -p 1 -v ./internal/<pkg>/...` per package and
  confirmed real `=== RUN` lines with zero `--- SKIP`. One package
  (`ucfcoverage`) failed on first run and was fixed correctly — no skip, no
  delete (see below).
- Measured per-package own-suite coverage with `-cover`, then ran the slice-396
  phantom check: the existing CI integration list (69 packages, none of which
  is a target) with `-coverpkg=./internal/<target>` to read the transitive
  contribution from other binaries.

## ci.yml GREP-TRAP note (slice 403 precedent honoured)

When extracting the CI integration package list for the phantom-measurement
script, the test-step lines (`sed -n '349,421p'`) were used ONLY — not a
whole-file grep — so the `./internal/api/...` tokens carried by the
`errleak-lint` + `duphelper-lint` `just` steps were never captured. The five
new entries were inserted into the "Run integration tests" `go test`
invocation, before the bare `./internal/api` root (which stays last, per the
slice-403 api-root form decision).

## Per-package outcomes

| Package                       | Broke?  | Own-suite         | Transitive-from-others | excludes action       | Floor                        |
| ----------------------------- | ------- | ----------------- | ---------------------- | --------------------- | ---------------------------- |
| `internal/api/questionnaires` | no      | 56.6%             | 5.6%                   | n/a (never gated)     | **54** (new) = floor(56.6−2) |
| `internal/api/ucfcoverage`    | YES (1) | 72.2%             | 3.0%                   | lifted off `excludes` | **70** = floor(72.2−2)       |
| `internal/api/emptyset`       | no      | `[no statements]` | n/a                    | KEEP on `excludes`    | none (zero-statement pkg)    |
| `internal/api/freshnessdrift` | no      | 83.6%             | 1.5%                   | lifted off `excludes` | **81** = floor(83.6−2)       |
| `internal/audit/notes`        | no      | 85.0%             | 0.6%                   | lifted off `excludes` | **83** = floor(85.0−2)       |

## `internal/api/ucfcoverage` — 1 test broke; fixed a STALE test (not a product bug)

`TestControlCoverage_Slice256_InScopeRowReturnsNumeric` failed:

```
integration_test.go:813: CC6.6 coverage = null; want strength*pass_rate (strength=0.7)
```

**Root cause (stale TEST, product code correct).** The test seeds a control
with the slice-002 legacy match-all `applicability_expr` (the empty string
`""`, which `scope.isLegacyTrueExpr` treats as "match every cell"). The
handler's `applyCoverage` (`internal/api/ucfcoverage/control_coverage.go`)
resolves a row's in-scope status via
`frameworkscope.EffectiveScope(applicability, predicate)`, where `applicability`
comes from `scope.Store.ControlApplicability` — the intersection of the
control's expr against **the tenant's scope-cell universe**
(`ListScopeCells`). The test never seeds a `scope_cells` row, so the universe
is empty, the match-all applicability resolves to an empty set,
`EffectiveScope` returns zero cells, the row renders out-of-scope, and coverage
is `null`. The two sibling slice-256 tests pass for unrelated reasons:
`OutOfScopeRowReturnsNull` activates no framework_scope (null is correct),
`NoEffectivenessDataReturnsNull` seeds no evaluations (null is correct). Only
the in-scope case requires a non-empty universe — and its setup never
established one.

Confirmed empirically: inserting one `scope_cells` row for `tenantA` and
re-running made the test pass with no other change. Confirmed it had never run
in CI: the package carried `//go:build integration` but was on the slice-345
`KNOWN_UNENROLLED` allowlist, so the gap had been latent since slice 256
(commit `da03fd24`) and survived only by relying on a stray scope-cell row in
whatever environment last ran it manually.

**Why a test fix, not a product fix, and not a spillover.** The product
behaviour is correct and documented: an empty scope-cell universe means nothing
is in scope, and `coverage: null` (not `0`) is the slice-256 P0 contract for
out-of-scope rows. The defect is wholly in the test's setup (an absent seed),
so the correct, unambiguous fix is to complete the setup — fix the test. This
is the slice's "stale test → fix the test" branch, not the "real product bug →
spillover" branch.

**Fix.** Added a `seedScopeCell(t, tenant)` helper (admin-pool INSERT of one
`scope_cells` row, mirroring the scope suite's pattern), called it in
`TestControlCoverage_Slice256_InScopeRowReturnsNumeric` before seeding
evaluations, and added `DELETE FROM scope_cells` to `wipeTenantState` (ordered
last, after `control_evaluations` which FK-references it `ON DELETE CASCADE`)
so the seeded cell does not leak into the out-of-scope / no-data tests within a
run. Full `ucfcoverage` suite green after the fix (18 sub-tests, zero SKIP,
zero FAIL); the sibling tests still pass because their null expectations hold
regardless of cell presence.

## `internal/api/emptyset` — zero-statement package, KEEP on excludes

`internal/api/emptyset` contains only `doc.go` (a package comment) — the actual
slice-150 cross-cutting empty-set robustness sweep lives in the external test
package `emptyset_test` (`audit_integration_test.go`). The package therefore
has **no executable statements**: `go test -cover` reports `coverage:
[no statements]`. A per-package line-coverage floor is undefined for a
zero-statement package; fabricating one would be dishonest. The package stays
on `excludes` with its existing slice-312 justification ("single-statement
empty-set sentinel helper — zero meaningful branches"). The integration suite
still runs in CI (it is the cross-cutting 5xx-on-empty-tenant audit); it just
contributes no own-package coverage to gate.

## Phantom check — all four floors are REAL (own-suite-dominated)

The slice-396 phantom test measures whether a floor is wholly the package's own
coverage or is inflated by transitive load from other binaries' suites. Running
the existing 69-package CI integration list (none of which is a target) with
`-coverpkg=./internal/<target>` yielded small transitive numbers in every case:
`questionnaires 5.6%`, `ucfcoverage 3.0%`, `freshnessdrift 1.5%`,
`audit/notes 0.6%` — each far below its own-suite number (56.6 / 72.2 / 83.6 /
85.0). The small non-zero transitive is expected (the cross-cutting `emptyset`
sweep and the dashboard / control-detail suites hit some of these handlers),
and is harmless: floors are anchored to the **own-suite** number at
`floor(own − 2)`, which is the conservative bound — the merged number (own +
transitive) can only be ≥ the floor, never below it. None of the four is a
transitive-load phantom (the slice-401 `internal/auth/users` pattern of
own-LOW / merged-HIGH); each floor is a real, stable ratchet earned by the
package's own suite.

(The full-list phantom run failed setup on ~33 packages locally — the
MinIO/NATS-dependent and SCF-seed-ordering packages — which is the known
local-only artifact the brief flagged; ~71 packages ran successfully, none of
which moved the target numbers above the own-suite figure. CI runs the list in
dependency order against a fresh DB with the SCF catalog imported first, so it
does not reproduce there. The phantom signal — that the target's own suite is
the dominant contributor — is unaffected.)

## Tests fixed

- `internal/api/ucfcoverage/integration_test.go` — added `seedScopeCell`
  helper, seeded one scope cell in `TestControlCoverage_Slice256_InScopeRowReturnsNumeric`,
  and added `scope_cells` to `wipeTenantState` cleanup (stale-test fix).

## Product fix

**None.** The `ucfcoverage` failure was a test-setup gap, not a product bug
(unlike slice 402's audit-export `CallerIsPrivileged` finding). The handler's
`null`-on-empty-universe behaviour is the documented slice-256 contract.

## Spillover

**None.** No genuine product bug requiring separate design work surfaced. The
single failure was a stale test, fixed in place.

## Coverage dispositions (summary)

- `internal/api/questionnaires`: floor **54** = `floor(56.6 − 2)`; new floor
  (never gated, never excluded — closes a gate gap); 5.6% transitive, own-suite-
  dominated → real.
- `internal/api/ucfcoverage`: floor **70** = `floor(72.2 − 2)`; lifted off
  `excludes`; 3.0% transitive → real.
- `internal/api/emptyset`: **kept on `excludes`** — zero-statement package
  (`[no statements]`); no honest floor exists.
- `internal/api/freshnessdrift`: floor **81** = `floor(83.6 − 2)`; lifted off
  `excludes`; 1.5% transitive → real.
- `internal/audit/notes`: floor **83** = `floor(85.0 − 2)`; lifted off
  `excludes`; 0.6% transitive → real.

(`internal/freshnessdrift` — the bare package at a different path — is NOT in
this batch and stays on `excludes` untouched.)

## KNOWN_UNENROLLED shrink

18 → 13 (removed the five batch-5 packages: `internal/api/emptyset`,
`internal/api/freshnessdrift`, `internal/api/questionnaires`,
`internal/api/ucfcoverage`, `internal/audit/notes`). Guard self-test 12/12
pass; `./scripts/audit-integration-enrolment.sh` reports OK with 74 enrolled /
13 waived. `scripts/check-coverage-excludes.sh` reports OK (45 excludes, all
justified, no orphans).

## Files touched

- `.github/workflows/ci.yml` — 5 package entries added to the integration job's
  "Run integration tests" step (all per-leaf recursive, inserted before the
  bare `./internal/api` root which stays last).
- `scripts/audit-integration-enrolment.sh` — 5 entries removed from
  `KNOWN_UNENROLLED` (18 → 13).
- `cmd/scripts/coverage-thresholds.json` — 4 floors added (`questionnaires 54`,
  `ucfcoverage 70`, `freshnessdrift 81` [api], `audit/notes 83`); 3 removed from
  `excludes` (`ucfcoverage`, api `freshnessdrift`, `audit/notes`) + their
  `$exclude_justifications` entries; `emptyset` kept on `excludes`
  (zero-statement); `questionnaires` was never gated and gains its first floor.
- `internal/api/ucfcoverage/integration_test.go` — `seedScopeCell` helper +
  in-scope-test seed + `wipeTenantState` scope_cells cleanup (stale-test fix).
- `CHANGELOG.md` — Unreleased › Changed entry.
- `docs/audit-log/405-integration-drain-batch-5-decisions.md` — this file.
