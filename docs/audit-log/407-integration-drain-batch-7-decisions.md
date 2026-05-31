# Slice 407 — integration-enrolment drain batch 7 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Batch 7 of the slice-390 drain. Enrols the four packages of the freshness /
drift / exception family that carry a `//go:build integration` tag but were
absent from the `tests-integration` job's package list in
`.github/workflows/ci.yml` (catalogued by the slice-345 guard's
`KNOWN_UNENROLLED` allowlist). Mirrors the slice-401..406 method (depended on
406 landing first so the batches stay sequential and CI stays green between
them).

## Scope

```
internal/drift
internal/freshness
internal/freshnessdrift
internal/exception
```

## Enrolment-form decisions

All four enrol as the **per-leaf recursive form `./internal/<pkg>/...`** —
NOT bare. Each directory is a single leaf package: one `integration_test.go`
plus its production source (`drift.go`, `freshness.go`, `worker.go`,
`expiry.go` + `store.go`) and **no tracked deeper subpackages** (`find
internal/<pkg> -type d` returns only the package dir itself). The bare form
(slice 403 api-root / slice 406 `internal/auth`) is reserved for a directory
holding only a root `_test.go` whose subpackages are tracked separately to
avoid double-enrolment; that condition does not hold here. This matches each
entry's listing in `KNOWN_UNENROLLED` (all four were bare `internal/<pkg>`
there, which the allowlist stores MINUS the trailing `/...` by design — the
script strips `/...` before comparing, so a bare allowlist line and a
`./internal/<pkg>/...` ci.yml line are the same package).

## ci.yml GREP-TRAP note (slice 403 precedent honoured)

The four new entries were inserted into the **"Run integration tests"** `go
test` invocation ONLY (immediately after `./internal/observability/otel/...`,
before the bare `./internal/api` root which stays last per the slice-403 form
decision). The `errleak-lint` + `duphelper-lint` `just` steps elsewhere in
ci.yml also carry `./internal/...` tokens; those were not touched. `actionlint`
on the edited file reports only pre-existing SC2034/SC2045 shellcheck warnings
in unrelated steps (lines 244/267/505 — the same warnings slice 406 noted, the
third shifted from 501→505 by this batch's five inserted lines); the
integration `go test` block edit is clean.

## Local harness

Stood up a CI-faithful harness: fresh `postgres:16-alpine` container
(`security-atlas-pg-407`, host port 55407), applied
`migrations/bootstrap/01-roles.sql` as the superuser, set
`atlas_app`/`atlas_migrate` passwords, then applied all 66 forward migrations
in order as `atlas_migrate` via plain `psql` (per the memory note that the
Atlas community build panics on apply). 87 tables created. All four suites need
only `DATABASE_URL_APP` (atlas_app role) + `DATABASE_URL` (atlas_migrate /
BYPASSRLS role); the `freshnessdrift` suite documents that it deliberately does
NOT exercise the NATS `RefreshSubscriber` (it needs a JetStream substrate and
the handler delegates to the same `Refresher.RefreshTenant` the scheduler path
already covers), so no NATS/MinIO was required for the per-package runs.

## Per-package outcomes

| Package                   | Broke? | Own-suite (integration) | excludes action       | Floor                    |
| ------------------------- | ------ | ----------------------- | --------------------- | ------------------------ |
| `internal/drift`          | no     | 86.0%                   | lifted off `excludes` | **84** = floor(86.0 − 2) |
| `internal/freshness`      | no     | 85.2%                   | lifted off `excludes` | **83** = floor(85.2 − 2) |
| `internal/exception`      | no     | 78.7%                   | lifted off `excludes` | **76** = floor(78.7 − 2) |
| `internal/freshnessdrift` | no     | 21.8%                   | lifted off `excludes` | **19** = floor(21.8 − 2) |

No test broke. This is the benign half of the slice-390 drain (cf. 406): four
never-run integration suites that pass cleanly on first enrolment.

## Combined serial run (reproduced CI conditions, not just isolation)

Per the slice-405 lesson (FK-wipe contamination is invisible in isolation), ran
the four packages together with the most likely contaminating neighbour
(`internal/demoseed`, which seeds rows under a shared tenant), serially:

```
go test -tags=integration -p 1 -count=1 \
  ./internal/demoseed/... \
  ./internal/drift/... \
  ./internal/freshness/... \
  ./internal/freshnessdrift/... \
  ./internal/exception/...
```

All GREEN, zero FAIL. **No FK-wipe contamination is structurally possible
here**, despite this family heavily referencing `controls` / `evidence_records`
/ `control_evaluations` (the exact RESTRICT-FK surface that bit `ucfcoverage`
in slice 405). Why: every test in all four packages scopes to a **fresh random
tenant** (`freshTenant(t, admin)` → `uuid.NewString()`), and every cleanup
`DELETE` is `WHERE tenant_id = $1` — tenant-scoped, never a global un-scoped
`DELETE FROM controls`. The slice-405 failure mode required a global parent
wipe that 23503-faults on another binary's rows under the _same_ canonical
tenant; with per-test random tenants and per-tenant cleanup that mode cannot
arise. This mirrors slice 406's `internal/auth` benign case.

## `internal/freshnessdrift` — own-low (21.8%) is REAL, not a transitive-load phantom

`freshnessdrift`'s 21.8% own-suite is low because the package
(`worker.go`, 286 LOC) carries a NATS JetStream `RefreshSubscriber` + a
`Scheduler.Run` loop alongside the DB-path `SweepOnce` / `RefreshTenant` the
suite exercises. The suite documents (integration_test.go:8-9) that it does
not drive the subscriber (it needs JetStream; the handler delegates to the
same `RefreshTenant`), so ~78% of the file is NATS-wiring the suite leaves
uncovered.

Crucially this is NOT the slice-401 `internal/auth/users` transitive-load
phantom (own-LOW / merged-HIGH because _other_ binaries load the package). Ran
the slice-404/405 phantom check — the enrolled suites that could plausibly
load it (`eval`, `metrics/scheduler`, `api/freshnessdrift`, `demoseed`) with
`-coverpkg=./internal/freshnessdrift/...` — and every one reported
`[no statements]`: **zero transitive coverage**. The only non-test caller is
`cmd/atlas/main.go`. So the merged CI profile = the package's own 21.8%, with
no inflation. That makes a `floor(21.8 − 2) = 19` floor an HONEST, real ratchet
on the DB-path coverage the suite genuinely earns — not a fabricated number
propped up by other binaries. Per the slice brief's matrix ("real own-coverage
→ per-package floor; transitive-load phantom → keep on excludes"), 21.8% is
real own-coverage and qualifies for a floor; it is not a phantom, so it does
not stay on `excludes`. The floor is deliberately low and documents the
NATS-subscriber gap; a future slice that stands up a JetStream test substrate
for the subscriber path can raise it.

## Phantom check — drift / freshness / exception floors are own-suite-dominated

`drift` (86.0), `freshness` (85.2), and `exception` (78.7) each have own-suite
integration coverage far above any plausible transitive contribution; with no
unit tests in any of the four packages (all report 0.0% unit), the merged
profile equals the integration-only number. Floors are anchored at
`floor(own − 2)`, the conservative bound: the merged number can only be ≥ the
floor, never below it.

## Tests fixed

**None.** All four suites passed unmodified, in isolation and in the combined
serial run.

## Product fix

**None.** No failure surfaced.

## Spillover

**None.** No package needed separate design work. (The `freshnessdrift` NATS
subscriber coverage gap is documented above as a known, honest floor rather
than spilled — raising it is a future JetStream-substrate test, not a product
or test-isolation bug.)

## Detection-tier classification

- `detection_tier_actual`: `none` — no bug surfaced during this slice.
- `detection_tier_target`: `none`.

## Coverage dispositions (summary)

- `internal/drift`: floor **84** = floor(86.0 − 2); lifted off `excludes`;
  own-suite-dominated → real.
- `internal/freshness`: floor **83** = floor(85.2 − 2); lifted off `excludes`;
  own-suite-dominated → real.
- `internal/exception`: floor **76** = floor(78.7 − 2); lifted off `excludes`;
  own-suite-dominated → real.
- `internal/freshnessdrift`: floor **19** = floor(21.8 − 2); lifted off
  `excludes`; own-low but REAL (phantom check = 0% transitive); the floor
  documents the deliberately-untested NATS subscriber path.

## KNOWN_UNENROLLED shrink

9 → 5 (removed the four batch-7 packages: `internal/drift`,
`internal/exception`, `internal/freshness`, `internal/freshnessdrift`). Guard
self-test 12/12 pass; `./scripts/audit-integration-enrolment.sh` reports OK
with 82 enrolled / 5 waived. `scripts/check-coverage-excludes.sh` reports OK
(41 excludes, all justified, no orphans) and its self-test passes 12/12.

## Files touched

- `.github/workflows/ci.yml` — 4 package entries added to the integration
  job's "Run integration tests" step (`./internal/drift/...`,
  `./internal/freshness/...`, `./internal/freshnessdrift/...`,
  `./internal/exception/...`; per-leaf recursive; inserted before the bare
  `./internal/api` root).
- `scripts/audit-integration-enrolment.sh` — 4 entries removed from
  `KNOWN_UNENROLLED` (9 → 5).
- `cmd/scripts/coverage-thresholds.json` — 4 floors added (`drift 84`,
  `exception 76`, `freshness 83`, `freshnessdrift 19`); 4 removed from
  `excludes` (`drift`, `exception`, `freshness`, `freshnessdrift`) + their
  `$exclude_justifications` entries.
- `CHANGELOG.md` — Unreleased › Changed entry.
- `docs/audit-log/407-integration-drain-batch-7-decisions.md` — this file.
