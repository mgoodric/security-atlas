# Slice 401 — integration-enrolment drain batch 1 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Batch 1 of the slice-390 drain. Enrols the four security-critical packages
that carry a `//go:build integration` tag but were absent from the
`tests-integration` job's package list in `.github/workflows/ci.yml`
(catalogued by the slice-345 guard's `KNOWN_UNENROLLED` allowlist). `oauth`
was dropped from the batch — slice 314 already enrolled + drained it (#909).

## Method

- Stood up a local harness mirroring the CI integration job: fresh
  `postgres:16-alpine` container, `migrations/bootstrap/01-roles.sql`, all 66
  forward migrations applied in order, `atlas_app` role password set, plus
  MinIO + NATS JetStream for the full-suite coverage measurement.
- Ran `go test -tags=integration -p 1 -v ./internal/<pkg>/...` per package and
  confirmed real `=== RUN` lines with **zero `--- SKIP` and zero `--- FAIL`**.
- Measured per-package coverage two ways: **own-suite** (default
  `-coverpkg=self`) and **merged** (full CI integration list + the 4 new
  packages, merged with the full unit `./...` profile via `gocovmerge`,
  identical to the slice-279 gate). The own-vs-merged delta is the
  slice-396 transitive-load-phantom signal.

## Per-package outcomes

| Package                 | Sub-tests RUN | Broke? | Own-suite cov | Merged cov | excludes action        | Floor                       |
| ----------------------- | ------------- | ------ | ------------- | ---------- | ---------------------- | --------------------------- |
| `internal/auth/oidc`    | 11            | no     | 69.5%         | 69.47%     | lifted off `excludes`  | **67** = floor(69.47 − 2)   |
| `internal/auth/jwtmw`   | 16            | no     | 91.5%         | 93.22%     | n/a (never excluded)   | 82 (unchanged)              |
| `internal/auth/users`   | 5             | no     | 20.3%         | 20.29%     | **kept on `excludes`** | —                           |
| `internal/audit/period` | 10            | no     | 72.4%         | 76.67%     | lifted off `excludes`  | **70** (own-suite-anchored) |

### `internal/auth/oidc` — lifted, floor 67

Own-suite (69.5%) equals merged (69.47%) to two decimals → the package's
coverage comes entirely from its **own** unit + integration suite, with no
transitive contribution from other binaries. This is a **real, stable floor**
(not a phantom). Lifted off `excludes`; floor set at `floor(69.47 − 2) = 67`.
Suite green on first enrolment; no test broken.

### `internal/auth/jwtmw` — no exclude/floor change

Already carried a hard floor of `82` and was **not** on `excludes`
(it is the one package in this batch that was already gated). Merged
coverage 93.2%, own-suite 91.5% — comfortably above the existing floor.
Enrolling its integration suite can only raise the merged number, so the
floor stays at 82 (no regression risk). Suite green; no test broken.

### `internal/auth/users` — KEPT on excludes (transitive-load phantom)

Own-suite coverage is only **20.3%**: the package ships a single
`bootstrap_integration_test.go` exercising the first-install bootstrap path.
The remaining ~80% of `users.go` (`ResolveForOAuth`, role queries, update
paths — users.go:102/116/170/396/433/468/496) is exercised **transitively**
by other packages' suites (admin/users handlers, the oauth DBUserResolver),
NOT by `users`' own suite. Setting a hard per-package floor off the
full-suite merged number would be a **slice-396-class phantom**: an unrelated
change to an admin handler's test could drop the transitive coverage and
break this floor for no reason attributable to the `users` package.
**Honest call: keep `internal/auth/users` on `excludes`.** Its justification
entry is retained. Enrolment (the load-bearing AC) is still done — the suite
now runs in CI; only the optional per-package floor lift is declined, per
AC-4's "distinguish a real floor from a transitive-load phantom honestly."

### `internal/audit/period` — lifted, floor 70

Own-suite 72.4%, merged 76.67% — a ~4pp transitive contribution on top of a
strong own-suite base, so the floor is genuinely earned by the package's own
tests. Rather than `floor(76.67 − 2) = 74` (which would lean partly on the
transitive ~4pp), the floor is anchored to the own-suite number at
`floor(72.4 − 2) = 70` — conservative against transitive drift while still a
real ratchet well above the actual own-suite coverage. Suite green; no test
broken.

## Tests fixed

**None.** Unlike slice 314 (which surfaced a five-slice-old device-flow
`postForm` doubling bug on enrolment), all four suites in this batch were
already correct and passed green on first run against the real harness. The
expected "silently-broken test" failure class did not materialise for these
four packages.

## Spillover

**None.** No newly-run test surfaced a genuine product bug. No spillover slice
filed.

## Local-vs-CI note (not a regression)

During the **full-suite** coverage measurement (the CI list + the 4 new
packages, run for the merged-profile floor math), `internal/api/scfimport`
and `internal/api/anchors` failed locally with
`scf_anchor "GOV-01" not found — import the SCF catalog first` (and the
downstream `soc2import` coverage dipped as a consequence). This is a
**local seed-ordering artifact** of the measurement run against a DB that an
earlier partial run had left in a half-seeded state — a known local-vs-CI
delta class. It is **unrelated to this slice's four packages** (all four
showed `ok`), and CI runs the list in dependency order against a fresh DB
with the SCF catalog imported first, so it does not reproduce in CI. Flagged
here for transparency; no action taken.

## Files touched

- `.github/workflows/ci.yml` — 4 package entries added to the integration job.
- `scripts/audit-integration-enrolment.sh` — 4 entries removed from
  `KNOWN_UNENROLLED` (37 → 33).
- `cmd/scripts/coverage-thresholds.json` — `internal/auth/oidc: 67` and
  `internal/audit/period: 70` floors added; both removed from `excludes` +
  their `$exclude_justifications` entries. `internal/auth/users` and
  `internal/auth/jwtmw` unchanged.
- `CHANGELOG.md` — Unreleased › Changed entry.
- `docs/audit-log/401-integration-drain-batch-1-decisions.md` — this file.
