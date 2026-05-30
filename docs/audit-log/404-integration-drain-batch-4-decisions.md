# Slice 404 — integration-enrolment drain batch 4 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Batch 4 of the slice-390 drain. Enrols the five api-domain-handler packages
that carry a `//go:build integration` tag but were absent from the
`tests-integration` job's package list in `.github/workflows/ci.yml`
(catalogued by the slice-345 guard's `KNOWN_UNENROLLED` allowlist). Mirrors the
slice-401/402/403 method (depended on 403 landing first so the batches stay
sequential and CI stays green between them).

## Scope

```
internal/api/board
internal/api/calendar
internal/api/policies
internal/api/vendors
internal/api/search
```

## Method

- Stood up a local harness mirroring the CI integration job: fresh
  `postgres:16-alpine` container (`security-atlas-pg-404`),
  `migrations/bootstrap/01-roles.sql`, all 70 forward migrations applied in
  order, `atlas_app` password set. None of these five packages touch
  MinIO/NATS directly — they are DB-backed handler suites needing only
  `DATABASE_URL_APP` (atlas_app role) + `DATABASE_URL` (admin role) — so no
  object store / queue was required.
- Ran `go test -tags=integration -p 1 -v ./internal/api/<pkg>/...` per package
  and confirmed real `=== RUN` lines with **zero `--- SKIP` and zero
  `--- FAIL`**. Sub-test RUN counts: board 25, calendar 25, policies 43,
  vendors 8, search 27.
- Measured coverage two ways per the slice-396 phantom check:
  **own-suite** (`-cover`, coverpkg=self) and a **transitive-from-others**
  measurement — ran the full existing CI integration list (the 64 packages
  ALREADY in the ci.yml test step, which does NOT include the target package)
  with `-coverpkg=./internal/api/<target>` and read the resulting profile. A
  non-zero transitive number would be a slice-396 transitive-load phantom; a
  zero means the package's coverage is wholly its own and the floor is real.

## ci.yml GREP-TRAP note (slice 403 precedent honoured)

When extracting the CI integration package list for the coverage-measurement
scripts, a naive whole-file `grep -oE '\./internal/...'` over `ci.yml` also
captures the `./internal/api/...` tokens carried by the `errleak-lint` and
`duphelper-lint` `just` steps — NOT integration-list entries. Including that
stray recursive `./internal/api/...` arg in a `go test` invocation produced a
spurious `[setup failed]` over the whole package set. The fix (and the correct
discipline going forward, per slice 403) is to extract the list from the
"Run integration tests" test-step lines ONLY (`sed -n '349,416p'`), which
yields the bare `./internal/api` token and no `/...` trap. All
measurement runs below used the test-step-scoped extraction.

## Per-package outcomes

| Package                 | Sub-tests RUN | Broke? | Own-suite | Transitive-from-others | excludes action       | Floor                          |
| ----------------------- | ------------- | ------ | --------- | ---------------------- | --------------------- | ------------------------------ |
| `internal/api/board`    | 25            | no     | 66.2%     | 0.0%                   | lifted off `excludes` | **64** = floor(66.2 − 2)       |
| `internal/api/calendar` | 25            | no     | 81.3%     | 0.0%                   | n/a (never excluded)  | **79** (new) = floor(81.3 − 2) |
| `internal/api/policies` | 43            | no     | 41.2%     | 0.0%                   | lifted off `excludes` | **39** = floor(41.2 − 2)       |
| `internal/api/vendors`  | 8             | no     | 60.6%     | 0.0%                   | lifted off `excludes` | **58** = floor(60.6 − 2)       |
| `internal/api/search`   | 27            | no     | 80.9%     | 0.0%                   | n/a (never excluded)  | **78** (new) = floor(80.9 − 2) |

All five suites passed GREEN on first enrolment against the real harness — the
"silently-broken test" failure class (which 314 and 402 each hit) did **not**
materialise for this batch (matching 401 + 403 — the variance the brief
anticipated). **No test fixed; no product bug found; no spillover filed.**

### Phantom check — all five are REAL floors (0.0% transitive)

This batch's phantom signal is unusually crisp. For every package, running the
ENTIRE existing CI integration list (64 packages, none of which is the target)
with `-coverpkg=./internal/api/<target>` yielded **0.0%** coverage of the
target's statements. That is the slice-396 phantom test passing in its
strongest form: not one statement of any of these five packages is exercised
by any other binary's integration suite. Each package's coverage comes wholly
from its own `integration_test.go`. The floors are therefore real, stable
ratchets — an unrelated change to another handler's test cannot move them.
(Contrast slice 401's `internal/auth/users`, kept on `excludes` precisely
because ~80% of its coverage was transitive load from other suites — a
genuine phantom. None of this batch's packages exhibit that.)

The floors are anchored conservatively to the own-suite number at
`floor(own_suite − 2)`, consistent with 401/402/403.

### excludes lift vs. new floor

- `board`, `policies`, `vendors` were on `excludes` (trailing-slash form,
  `internal/api/<pkg>/`) with a `$exclude_justifications` entry. All three are
  lifted off `excludes`, their justification entries removed, and gain a hard
  per-package floor.
- `calendar` and `search` were NEVER on `excludes` and carried no floor — they
  slipped through the gate entirely (neither gated nor waived). Each gains its
  first hard floor. This closes a real gap: prior to this slice their coverage
  was unconstrained.

### `internal/api/policies` at 41.2% — low but honest, not a phantom

`policies` is the low outlier. Its two integration suites
(`empty_set_integration_test.go`, `ack_rate_integration_test.go`) exercise the
empty-set and acknowledgment-rate paths; the export and other handlers in
`handlers.go` / `export.go` carry less own-coverage. Crucially the
transitive-from-others number is 0.0%, so this is **not** a transitive-load
phantom — the honest disposition is a real, conservative floor of 39, not a
kept-exclude. The low absolute number is an absence of tests, not a defect;
it is a candidate for a future policies-handler integration-test slice (NOT a
product bug, NOT filed as spillover, per the slice's spillover policy).

## Tests fixed

**None.** All five suites were already correct and passed green on first run
against the real harness.

## Product fix

**None.** No newly-run test surfaced a product bug (unlike 402's audit-export
`CallerIsPrivileged` finding).

## Spillover

**None.** No genuine product bug requiring separate design work surfaced. The
`policies` low own-coverage is a test-coverage gap, not a defect; noted above
for a possible future slice but not filed (per policy: spillover is for product
bugs needing design work).

## Coverage dispositions (summary)

- `internal/api/board`: floor **64** = `floor(66.2 − 2)`; lifted off
  `excludes`; 0.0% transitive → real.
- `internal/api/calendar`: floor **79** = `floor(81.3 − 2)`; new floor (never
  excluded, never gated); 0.0% transitive → real.
- `internal/api/policies`: floor **39** = `floor(41.2 − 2)`; lifted off
  `excludes`; 0.0% transitive → real (phantom-free), low but honest.
- `internal/api/vendors`: floor **58** = `floor(60.6 − 2)`; lifted off
  `excludes`; 0.0% transitive → real.
- `internal/api/search`: floor **78** = `floor(80.9 − 2)`; new floor (never
  excluded, never gated); 0.0% transitive → real.

## KNOWN_UNENROLLED shrink

23 → 18 (removed the five batch-4 packages: `internal/api/board`,
`internal/api/calendar`, `internal/api/policies`, `internal/api/search`,
`internal/api/vendors`). Guard self-test 12/12 pass;
`./scripts/audit-integration-enrolment.sh` reports OK with 69 enrolled / 18
waived.

## Files touched

- `.github/workflows/ci.yml` — 5 package entries added to the integration job's
  "Run integration tests" step (all per-leaf recursive, inserted before the
  bare `./internal/api` root which stays last).
- `scripts/audit-integration-enrolment.sh` — 5 entries removed from
  `KNOWN_UNENROLLED` (23 → 18).
- `cmd/scripts/coverage-thresholds.json` — 5 floors added (`board 64`,
  `calendar 79`, `policies 39`, `search 78`, `vendors 58`); 3 removed from
  `excludes` (`board`, `policies`, `vendors`) + their `$exclude_justifications`
  entries; `calendar` and `search` were never excluded and gain first floors.
- `CHANGELOG.md` — Unreleased › Changed entry.
- `docs/audit-log/404-integration-drain-batch-4-decisions.md` — this file.
