# Slice 403 — integration-enrolment drain batch 3 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Batch 3 of the slice-390 drain. Enrols the next five packages that carry a
`//go:build integration` tag but were absent from the `tests-integration`
job's package list in `.github/workflows/ci.yml` (catalogued by the slice-345
guard's `KNOWN_UNENROLLED` allowlist). Mirrors the slice-401/402 method
(depended on 402 landing first so the batches stay sequential and CI stays
green between them).

## Scope

```
internal/api/adminusers
internal/api/aggregationrules
internal/api
internal/api/me
internal/api/decisions
```

## Method

- Stood up a local harness mirroring the CI integration job: fresh
  `postgres:16-alpine` container, `migrations/bootstrap/01-roles.sql`, all 66
  forward migrations applied in order, `atlas_app` password set, plus MinIO +
  NATS JetStream available for the full-suite merged-coverage measurement.
- Ran `go test -tags=integration -p 1 -v ./internal/api/<pkg>/...` per child
  package (and `go test -tags=integration -p 1 -v ./internal/api/` for the
  root, NOT `/...`) and confirmed real `=== RUN` lines with **zero `--- SKIP`
  and zero `--- FAIL`** in isolation.
- Measured per-package coverage two ways per the slice-396 phantom check:
  **own-suite** (default `-coverpkg=self`) and **merged** (full CI integration
  list + the 5 new entries, merged with the full unit `./...` profile via
  `gocovmerge`, then read through `cmd/scripts/coverage-gate` — identical to
  the slice-279 gate). The own-vs-merged delta is the transitive-load-phantom
  signal.

## api-root enrolment FORM (the real judgment call)

The spec flagged this as the load-bearing decision: `internal/api` is the
package-root, and enrolling it as `./internal/api/...` (recursive) would
re-run **every** `internal/api/*` child suite a second time (they are already
enrolled individually as `./internal/api/<child>/...`). The CI list is
deliberately per-leaf-package.

**Decision: enrol the root as the bare, non-recursive token `./internal/api`
(no `/...`).** This matches the **existing precedent already in the list**:
`./internal/audit` (the audit package-root) sits in the list as a bare token
alongside its recursive children `./internal/audit/sink/...`,
`./internal/audit/unifiedlog/...`, etc. The bare form runs ONLY the root
package's own `_test.go` files, never its children. The slice-345 guard parses
this correctly — its regex `\./internal/[A-Za-z0-9_/]+(/\.\.\.)?` matches the
bare token and `sed` strips it to `internal/api`, which is exactly the
allowlist key being removed. Placed last in the list (the final entry omits
the trailing `\`), consistent with the block's shape.

Rejected alternative `./internal/api/...`: double-runs all sibling suites,
inflates wall-clock, and is inconsistent with the list's per-leaf granularity
and the `./internal/audit` precedent.

## Per-package outcomes

| Package                         | Broke? | Own-suite | Merged (gate) | excludes action       | Floor              |
| ------------------------------- | ------ | --------- | ------------- | --------------------- | ------------------ |
| `internal/api`                  | no     | 70.9%     | 74.4%         | n/a (never excluded)  | **72** (unchanged) |
| `internal/api/adminusers`       | no     | 63.2%     | 63.2%         | lifted off `excludes` | **61** (new)       |
| `internal/api/aggregationrules` | no     | 61.5%     | 61.5%         | lifted off `excludes` | **59** (new)       |
| `internal/api/me`               | no     | 45.8%     | 45.8%         | lifted off `excludes` | **43** (new)       |
| `internal/api/decisions`        | no     | 20.6%     | 20.6%         | lifted off `excludes` | **18** (new)       |

All five suites passed GREEN on first enrolment against the real harness — the
expected "silently-broken test" failure class (which 314 and 402 each hit) did
**not** materialise for this batch. **No test fixed; no product bug found; no
spillover filed.**

### `internal/api` (root) — floor unchanged at 72

The root package was **already enrolled in the coverage gate at floor 72** (it
is the one batch member that was never on `excludes`) — but its integration
suite (`legacy_bearer_retirement_test.go`, the slice-197 legacy-bearer
retirement + evidence-push + credentials integration tests) was **never in the
ci.yml integration package list**, so it had never run in CI. Enrolment is the
move here.

Merged coverage is 74.4% (own-suite 70.9% + ~3.5pp transitive from sibling
suites that exercise root-package handlers). The existing floor of 72 already
sits between own-suite and merged. Enrolling the integration suite can only
**raise** the merged number, so there is no regression risk to the 72 floor.
Per the monotonic-ratchet contract, an existing higher floor is never lowered:
72 stays. (Anchoring conservatively to own-suite would give `floor(70.9 − 2) =
68`, which is _below_ the live 72 — lowering it would violate the ratchet, so
72 is correct and is left untouched.)

### `internal/api/adminusers`, `aggregationrules`, `me`, `decisions` — all four: own == merged (no phantom), real floors

For all four children, **merged coverage equals own-suite coverage to the
decimal** (63.2/63.2, 61.5/61.5, 45.8/45.8, 20.6/20.6). That equality is the
slice-396 phantom test passing: the package's coverage comes **entirely from
its own suite**, with no transitive contribution from other binaries. A floor
anchored to that number is a **real, stable ratchet**, not a phantom — an
unrelated change elsewhere cannot move it. Each is lifted off `excludes` (its
`$exclude_justifications` entry removed) and gains a hard floor at
`floor(own_suite − 2)`.

Note `internal/api/me` ships two small unit files (`profile_contract_test.go`,
`tenants_test.go`); the others ship only an integration suite. Even so, all
four merged numbers are own-suite-dominated, so the own==merged equality holds
and the floors are honestly earned.

`internal/api/decisions` at 20.6% is the low outlier: its integration suite
(`filters_integration_test.go`) exercises `ListDecisions`' filter logic, while
the other handlers (`CreateDecision`, `GetDecision`, `UpdateDecision`,
`Supersede`, `AddLink`, `RemoveLink`, …) sit at 0% own-coverage. Crucially this
is **not** a transitive-load phantom (own == merged == 20.6%, so nothing else
covers them either): the honest disposition is a **real, conservative floor of
18**, not a kept-exclude. Contrast slice 401's `internal/auth/users`, which was
kept on `excludes` precisely **because** its own-suite (20.3%) differed from
its transitively-driven merged number — a phantom. Here there is no phantom, so
the floor is set and is phantom-free. The low absolute coverage is a candidate
for a future decisions-handler integration-test slice (NOT a product bug, NOT
filed as spillover — it is an absence of tests, not a defect).

## Tests fixed

**None.** All five suites were already correct and passed green on first run
against the real harness.

## Product fix

**None.** No newly-run test surfaced a product bug (unlike 402's audit-export
`CallerIsPrivileged` finding).

## Spillover

**None.** No genuine product bug requiring separate design work surfaced. The
`decisions` low own-coverage is a test-coverage gap, not a defect; it is noted
above for a possible future slice but not filed (per policy: spillover is for
product bugs needing design work).

## Coverage dispositions (summary)

- `internal/api`: floor **72 unchanged** (already gated; never excluded;
  merged 74.4% ≥ 72; enrolment only raises merged; ratchet forbids lowering).
- `internal/api/adminusers`: floor **61** = `floor(63.2 − 2)`; lifted off
  `excludes`; own == merged → real.
- `internal/api/aggregationrules`: floor **59** = `floor(61.5 − 2)`; lifted off
  `excludes`; own == merged → real.
- `internal/api/me`: floor **43** = `floor(45.8 − 2)`; lifted off `excludes`;
  own == merged → real.
- `internal/api/decisions`: floor **18** = `floor(20.6 − 2)`; lifted off
  `excludes`; own == merged → real (phantom-free), low but honest.

## KNOWN_UNENROLLED shrink

28 → 23 (removed the five batch-3 packages: `internal/api`,
`internal/api/adminusers`, `internal/api/aggregationrules`,
`internal/api/decisions`, `internal/api/me`). Guard self-test 12/12 pass;
`./scripts/audit-integration-enrolment.sh` reports OK with 64 enrolled / 23
waived.

## Local-vs-CI note (not a regression)

During the **full-suite** merged-coverage measurement (the CI list + the 5 new
entries, run for the floor math), `internal/api/anchors` failed locally with
`scf_anchor "GOV-01" not found — import the SCF catalog first (slice 006)`, and
the downstream `internal/api/soc2import` merged coverage consequently dipped to
64.2% (below its live 75 floor). This is the **known local-only seed-ordering
artifact** documented in slice 401's decisions log and called out in this
slice's brief: a half-seeded local DB left by an earlier partial measurement
run. It is **unrelated to this slice's five packages** (all five passed in
isolation and all five pass their floors against the merged profile), and CI
runs the list in dependency order against a fresh DB with the SCF catalog
imported first, so it does not reproduce in CI. Flagged for transparency; no
action taken.

## Files touched

- `.github/workflows/ci.yml` — 5 package entries added to the integration job
  (`adminusers`, `aggregationrules`, `me`, `decisions` recursive;
  `internal/api` root bare/non-recursive per the `./internal/audit` precedent).
- `scripts/audit-integration-enrolment.sh` — 5 entries removed from
  `KNOWN_UNENROLLED` (28 → 23).
- `cmd/scripts/coverage-thresholds.json` — 4 floors added (`adminusers 61`,
  `aggregationrules 59`, `me 43`, `decisions 18`); 4 removed from `excludes` +
  their `$exclude_justifications` entries; `internal/api` floor unchanged at 72.
- `CHANGELOG.md` — Unreleased › Changed entry.
- `docs/audit-log/403-integration-drain-batch-3-decisions.md` — this file.
