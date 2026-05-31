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

## `internal/api/ucfcoverage` — SECOND find: FK-wipe test-isolation bug (CI-only), fixed with TRUNCATE CASCADE

After the PR's first push, CI run `26697205353` failed the ENTIRE
`internal/api/ucfcoverage` suite (all 14 top-level tests), each with:

```
wipe controls: ERROR: update or delete on table "controls" violates foreign key
constraint "evidence_records_tenant_id_control_id_fkey" on table "evidence_records" (SQLSTATE 23503)
```

This is a textbook slice-390-thesis hit — a latent test-isolation bug that was
invisible while the package ran only in isolation and surfaced the moment it
ran in CI's serial `-p 1` job alongside its neighbours. The integration job
runs `-p 1` serially and `./internal/demoseed/...` (plus the audit-sample
suites) run before `ucfcoverage`, leaving rows under the SAME canonical
`tenantA` (`11111111-…`) that FK-reference controls. The package's own
un-tenant-restricted global `DELETE FROM controls` then 23503-faults.

**Root cause (test-harness, NOT a product bug).** The wipe helpers deleted the
`controls` parent without clearing its FK dependents. The relevant FKs are
`ON DELETE RESTRICT` (not CASCADE), so a single referencing row blocks the
delete. The FK closure of `controls` is deep and wide.

**First attempt (enumerate-children) was the WRONG strategy.** The initial fix
added explicit child deletes (`evidence_records`, `control_evaluations`) before
`controls`. CI then advanced the error exactly ONE level deeper:

```
wipe (DELETE FROM evidence_records): ERROR: update or delete on table "evidence_records"
violates foreign key constraint "sample_evidence_evidence_fk" on table "sample_evidence" (SQLSTATE 23503)
```

The FK chain is `sample_evidence → evidence_records → controls`, all RESTRICT.
Introspecting `migrations/` shows MANY tables FK-reference controls directly or
transitively with RESTRICT (`evidence_records`, `audit_samples` /
`sample_evidence`, `walkthroughs`, `exceptions`, `risk_register`,
`freshness_drift`, …). Enumerating children is whack-a-mole: each missed level
costs a ~13-minute CI round and may still miss the next. Tenant-scoping the
wipe does not help either — the contamination is under the same `tenantA`.

**Robust fix — TRUNCATE … CASCADE (established repo precedent, not novel).**
`TRUNCATE controls … CASCADE` truncates the entire transitive dependent set
atomically, ignoring RESTRICT, in one statement. It does NOT touch `scf_anchors`
or `tenants` (controls REFERENCES those — child→parent — so they are not
dependents; verified by grepping the migration FKs). This mirrors the existing
pattern at `internal/evidence/ingest/integration_test.go:116`,
`internal/evidence/streambuf/integration_test.go:110`, and
`internal/platform/status_integration_test.go:253` (all `TRUNCATE … CASCADE`
via the admin pool, which owns the tables — privilege is fine).

- `wipeTenantControls`: `TRUNCATE controls RESTART IDENTITY CASCADE` (kept the
  global, un-tenant-restricted form — cross-tenant tests need both tenants
  cleared; CASCADE handles the full dependent closure).
- `wipeTenantState`: `TRUNCATE controls, framework_scopes, scope_cells RESTART
IDENTITY CASCADE` — `controls` CASCADE clears `control_evaluations` +
  `evidence_records` + their children; `framework_scopes` and `scope_cells` are
  NOT control-dependents, so they are listed explicitly (the latter so the
  in-scope test's seeded cell does not leak into the out-of-scope / no-data
  tests within a run).

No product change; the RESTRICT FKs are correct product behaviour (a control
cannot be deleted while evidence references it). The `70` floor for
`ucfcoverage` is unchanged.

**Verification (reproduced the DEEPER CI condition, not just isolation).**
Against a fresh harness, ran `internal/demoseed` first, then seeded the full
contaminating chain under `tenantA`: an `evidence_records` row referencing a
control PLUS a `population → sample → sample_evidence` chain pinning that
evidence record (i.e. the exact `sample_evidence → evidence_records → controls`
RESTRICT closure CI hit). Ran the FIXED suite with
`go test -tags=integration -p 1 -count=1 ./internal/demoseed/...
./internal/api/ucfcoverage/...`: both packages GREEN, zero FAIL, no FK error.
Confirmed the TRUNCATE CASCADE actually cleared the contamination —
`evidence_records` 1→0 and `sample_evidence` 1→0 after the run.

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

- `internal/api/ucfcoverage/integration_test.go` (find 1, local) — added
  `seedScopeCell` helper, seeded one scope cell in
  `TestControlCoverage_Slice256_InScopeRowReturnsNumeric`, and added
  `scope_cells` to `wipeTenantState` cleanup (stale-test fix).
- `internal/api/ucfcoverage/integration_test.go` (find 2, CI-only) — replaced
  both wipe helpers' enumerated `DELETE`s with `TRUNCATE … CASCADE`
  (`wipeTenantControls`: `TRUNCATE controls RESTART IDENTITY CASCADE`;
  `wipeTenantState`: `TRUNCATE controls, framework_scopes, scope_cells RESTART
IDENTITY CASCADE`), fixing the RESTRICT-FK 23503 failures whose chain
  (`sample_evidence → evidence_records → controls`) is deeper than a fixed
  child enumeration handles. Mirrors the repo's existing TRUNCATE-CASCADE
  precedent. Surfaced when the package ran in CI's serial `-p 1` job after
  packages that seed evidence/sample rows under the shared tenant.

## Product fix

**None.** Both `ucfcoverage` failures were test-harness defects, not product
bugs (unlike slice 402's audit-export `CallerIsPrivileged` finding). Find 1's
`null`-on-empty-universe behaviour is the documented slice-256 contract; find
2's `ON DELETE RESTRICT` FK is correct product behaviour (a control cannot be
deleted while evidence references it) — the bug was that the test deleted
parents before children.

## Spillover

**None.** Neither failure needed separate design work. Both were
test-isolation / setup defects fixed in place — the exact class of latent bug
the slice-390 enrolment drain exists to surface.

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
- `internal/api/ucfcoverage/integration_test.go` — find 1: `seedScopeCell`
  helper + in-scope-test seed + `wipeTenantState` scope_cells cleanup; find 2:
  replaced both wipe helpers with `TRUNCATE … CASCADE` (robust over the deep
  RESTRICT FK closure `sample_evidence → evidence_records → controls`),
  matching repo precedent.
- `CHANGELOG.md` — Unreleased › Changed entry.
- `docs/audit-log/405-integration-drain-batch-5-decisions.md` — this file.
