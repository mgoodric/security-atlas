# Coverage audit — 2026-05 (slice 279)

> **Purpose.** Slice 279 audits every Go package below 70% in
> `cmd/scripts/coverage-thresholds.json` against a re-measurement that
> INCLUDES integration coverage (`go test -tags=integration -coverpkg=./...`).
> The big finding: many "low" packages are heavily exercised by
> integration tests but the coverage gate (unit-only profile) never
> counted them. After merging unit + integration profiles, ~9
> packages already sit above 70% without writing a single new test
> — they need a FLOOR-ONLY lift. This doc enumerates every below-70
> package with a per-package disposition.
>
> Methodology:
>
> 1. Unit profile: `go test -coverpkg=./... -coverprofile=unit.cov ./...`
> 2. Integration profile: `go test -tags=integration -p 1 -coverpkg=./... -coverprofile=integration.cov <CI test packages>`
> 3. Merge via `gocovmerge unit.cov integration.cov > merged.cov`
> 4. Per-package totals computed from the merged profile.
>
> Floor methodology (per slice 069): `max(0, floor(merged_pct - 2pp))`.

## Headline numbers

| Surface                                            | Count                                           |
| -------------------------------------------------- | ----------------------------------------------- |
| Total Go packages in `coverage-thresholds.json`    | 76                                              |
| Below 70% in `thresholds` (merged) — this audit    | 31                                              |
| `unit-add` (need new tests)                        | 22                                              |
| `count-integration` (≥70% merged; floor-only lift) | 9                                               |
| `exempt` (cmd glue, no business logic to test)     | (covered in tiered guidance, not relisted here) |

The original "43 of 76 below 70%" framing in the slice spec was
based on unit-only measurement. Integration-aware re-measurement
moves ~12 packages above 70% (the `count-integration` set plus
several that came from re-measuring more accurately).

## Five lift targets (this slice writes the tests)

These are the highest-leverage `unit-add` packages — chosen by
combining gap-to-70%, business criticality, and statement count
(smaller packages = lower cost to hit 70%):

| Package                   | Unit-only % | Merged % | Statements | Gap to 70% | Rationale                                                          |
| ------------------------- | ----------- | -------- | ---------- | ---------- | ------------------------------------------------------------------ |
| `internal/decision`       | 4.2         | 67.8     | 451        | 2.2 pp     | Core decision-engine logic; tiny push to clear bar                 |
| `internal/frameworkscope` | 21.4        | 21.8     | 248        | 48.2 pp    | RLS-relevant; small package, focused predicate + canonical surface |
| `internal/eval`           | 16.0        | 31.4     | 363        | 38.6 pp    | Control-eval engine; rego + state + engine — moderate size         |
| `internal/risk`           | 12.1        | 36.1     | 820        | 33.9 pp    | Risk-register core; methodology + severity + treatment surfaces    |
| `internal/board`          | 23.2        | 23.7     | 734        | 46.3 pp    | Board-pack composer; narrative + generator + pack — large package  |

Lift targets (D1 in the decisions log) match the slice spec's
"Provisional 5" exactly — the audit measurement did not displace any
of the originals. `internal/decision` is the easy win (already 67.8%
merged); the other four require substantive unit-test additions.

## Below-70% packages — full table

### `unit-add` — packages requiring NEW unit tests

These are below 70% even after merging integration coverage.
Spillover slices (282-303) file each remaining package not in the 5
lift targets.

| Package                                    | Unit-only % | Merged % | Statements | Disposition    | Spillover # | Notes                                                                  |
| ------------------------------------------ | ----------- | -------- | ---------- | -------------- | ----------- | ---------------------------------------------------------------------- |
| `internal/decision`                        | 4.2         | 67.8     | 451        | unit-add       | LIFT (this) | tiny gap; narrative + overdue + store helpers covered in 5-target lift |
| `internal/frameworkscope`                  | 21.4        | 21.8     | 248        | unit-add       | LIFT (this) | predicate.go + canonical.go pure logic; lift target                    |
| `internal/eval`                            | 16.0        | 31.4     | 363        | unit-add       | LIFT (this) | engine + rego + state; lift target                                     |
| `internal/risk`                            | 12.1        | 36.1     | 820        | unit-add       | LIFT (this) | methodology + severity + treatment; lift target                        |
| `internal/board`                           | 23.2        | 23.7     | 734        | unit-add       | LIFT (this) | narrative + generator + pack; lift target                              |
| `internal/scope`                           | 35.0        | 35.3     | 266        | unit-add       | 282         | scope-cell predicate logic; pure-Go unit-testable                      |
| `internal/oscal`                           | 41.4        | 41.4     | 256        | unit-add       | 283         | OSCAL ingest/export marshalling; pure-data unit-testable               |
| `internal/risk/aggrule`                    | 18.9        | 19.1     | 418        | unit-add       | 284         | risk aggregation rules; pure logic; sibling to risk lift               |
| `internal/observability/otel`              | 15.8        | 15.8     | 139        | unit-add       | 285         | tracing wrappers; surface is small; quick unit win                     |
| `internal/vendor`                          | 9.5         | 10.1     | 179        | unit-add       | 286         | vendor data model + helpers                                            |
| `internal/audit/walkthrough`               | 3.0         | 6.0      | 418        | unit-add       | 287         | walkthrough store helpers; large package                               |
| `internal/artifact`                        | 5.7         | 5.7      | 122        | unit-add       | 288         | artifact metadata + redact helpers; small                              |
| `internal/api/controldetail`               | 25.0        | 29.3     | 273        | unit-add       | 289         | control-detail HTTP handler logic                                      |
| `internal/api/controls`                    | 26.0        | 26.3     | 559        | unit-add       | 290         | controls HTTP handler — large surface                                  |
| `internal/api/oscalexport`                 | 37.0        | 39.4     | 33         | unit-add       | 291         | OSCAL export HTTP handler; small surface                               |
| `internal/api/metrics`                     | 0.0         | 0.4      | 275        | unit-add       | 292         | metrics endpoint; needs handler + auth tests                           |
| `internal/metrics/eval`                    | 33.0        | 33.0     | 88         | unit-add       | 293         | eval metric reducer; small                                             |
| `internal/metrics/scheduler`               | 0.0         | 0.0      | 74         | unit-add       | 294         | scheduler stub; surface small but uncovered                            |
| `internal/catalog/metrics`                 | 0.0         | 64.7     | 153        | unit-add       | 295         | catalog metric emitter; needs unit on the entry helpers                |
| `connectors/aws/cmd/aws-connector`         | 9.0         | 9.0      | 111        | exempt-leaning | 296         | cobra glue + main; integration-tested; tier 'CLI cmd'                  |
| `connectors/jira/cmd/atlas-jira`           | 30.4        | 30.4     | 158        | exempt-leaning | 297         | cobra glue; tier 'CLI cmd'                                             |
| `connectors/github/cmd/atlas-github`       | 15.1        | 15.1     | 199        | exempt-leaning | 298         | cobra glue; tier 'CLI cmd'                                             |
| `connectors/okta/cmd/atlas-okta`           | 20.7        | 20.7     | 188        | exempt-leaning | 299         | cobra glue; tier 'CLI cmd'                                             |
| `connectors/osquery/cmd/atlas-osquery`     | 28.2        | 28.2     | 142        | exempt-leaning | 300         | cobra glue; tier 'CLI cmd'                                             |
| `connectors/1password/cmd/atlas-1password` | 14.8        | 14.8     | 108        | exempt-leaning | 301         | cobra glue; tier 'CLI cmd'                                             |
| `connectors/manual/cmd/atlas-manual`       | 43.6        | 43.6     | 283        | exempt-leaning | 302         | cobra glue; tier 'CLI cmd'                                             |
| `connectors/aws/internal/awsauth`          | 66.7        | 66.7     | 36         | unit-add       | 303         | tiny gap (3.3pp); quick unit additions                                 |

### `count-integration` — already ≥ 70% merged; floor-only lift in this slice

These packages are heavily exercised by integration tests; their
unit-only floor in `coverage-thresholds.json` simply hadn't been
updated to count integration. This slice raises the floor to
`floor(merged_pct - 2pp)`. NO new tests are written for these
— the audit IS the documentation of why the floor jumped.

| Package                       | Unit-only % | Merged % | Floor (before) | Floor (after) | Notes                                                               |
| ----------------------------- | ----------- | -------- | -------------- | ------------- | ------------------------------------------------------------------- |
| `internal/api`                | 11.0        | 71.9     | 11             | 69            | core API helpers; integration-exercised via every endpoint test     |
| `internal/api/credstore`      | 24.0        | 71.0     | 24             | 69            | OAuth credential store; slice 062 integration coverage              |
| `internal/api/schemaregistry` | 26.0        | 73.8     | 26             | 71            | schema registry HTTP; slice 068 integration coverage                |
| `internal/authz`              | 29.0        | 72.7     | 29             | 70            | OPA authz wiring; every integration test exercises this             |
| `internal/control`            | 47.0        | 74.1     | 47             | 72            | control store; slice 064 integration tests                          |
| `internal/featureflag`        | 23.0        | 84.7     | 23             | 82            | feature-flag store; slice 011 + admin features integration          |
| `internal/tenancy`            | 51.0        | 92.3     | 51             | 90            | tenancy context plumbing; every RLS integration test exercises this |

### Already covered (≥ 70% threshold passes, no action this slice)

The audit lists below-70% packages only. Packages already at or above
their floor are not relisted here. The full coverage gate at
`cmd/scripts/coverage-gate` enforces those on every CI run.

## Exempt packages (tier: CLI cmd glue)

Per the slice's tiered-floor doctrine, `cmd/atlas`, `cmd/atlas-cli`,
`cmd/atlas-openapi`, `cmd/atlas-oscal`, and each connector's
`cmd/atlas-*` binary are tier "CLI cmd glue" — cobra wiring + main
entry. They are integration-tested via the self-host bundle smoke
job (`cmd/atlas`, `cmd/atlas-cli`) and connector-specific live runs.
Their per-package floors stay at their current measured value; the
tiered guidance documents the rationale.

The audit lists CLI cmd packages with `exempt-leaning` disposition
but ALSO files spillover slices for them — a future maintainer may
decide to add `func TestMain_ParsesFlags` unit tests if the cobra
glue gets non-trivial, and the spillover slot reserves the work for
that day.

## Methodology notes (for future audits)

- **Why `gocovmerge`:** the upstream-blessed tool for merging
  multiple Go cover profiles; pinned via
  `go install github.com/wadey/gocovmerge@latest`. Alternatives
  (`gocov`, hand-rolled awk) are heavier or brittler. See D2.
- **Why `-coverpkg=./...` everywhere:** without it, the integration
  profile only counts statements in packages whose tests RAN. With
  it, statements in transitive dependencies (e.g., `internal/api`
  called from `internal/api/dashboard` integration tests) are
  counted. This is the primary mechanism that moved 7 packages from
  below-70% (unit-only) to above-70% (merged) without test additions.
- **Float-vs-pp:** floors are stored as integers (`floor(merged - 2pp)`).
  The 2pp band absorbs flaky-coverage noise (e.g., a test that
  conditionally covers a branch on a code path that depends on
  random ordering). Slice 069 P0-A4 codified this.
- **CI runtime impact:** the integration job already produces an
  `-coverpkg=./...` profile. The new step is: run the unit profile
  with `-coverpkg=./...` too (was unit-only), then merge in CI.
  Estimated +30s on the `Go · build + test` job. Acceptable.
  Tracked in D4.

## Provenance

- Filed 2026-05-25 from slice 279 measurements at
  commit `<commit>` (see PR for the exact sha).
- Re-measurement reproducible via:
  ```bash
  go test -coverpkg=./... -coverprofile=unit.cov ./...
  go test -tags=integration -p 1 -coverpkg=./... \
    -coverprofile=integration.cov \
    ./internal/db/... ./internal/api/scfimport/... ./internal/api/anchors/... \
    ./internal/api/schemaregistry/... ./internal/featureflag/... \
    ./internal/api/features/... ./internal/decision/... \
    ./internal/api/dashboard/... ./internal/api/risks/... \
    ./internal/authz/... ./internal/control/... ./internal/platform/...
  gocovmerge unit.cov integration.cov > merged.cov
  go tool cover -func=merged.cov
  ```
- Maintainer revisits this audit when:
  - The next batch of spillover lifts (282-303) lands — re-measure to
    confirm the floor moves matched expectations.
  - Frontend (vitest) coverage gate ships — sister audit needed.
  - A new package lands without a threshold row — the audit policy
    is "every package, every audit, no holes".
