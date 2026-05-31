# Slice 408 — integration-enrolment drain batch 8 (decisions log)

**Type:** AFK · **Parent:** 390 · **Cluster:** infra

Eighth and **final** batch of the slice-390 drain. Enrols the last five
packages that carry a `//go:build integration` tag but were absent from the
`tests-integration` job's package list in `.github/workflows/ci.yml`
(catalogued by the slice-345 guard's `KNOWN_UNENROLLED` allowlist). Mirrors the
slice-401..407 method (depended on 407 landing first so the batches stay
sequential and CI stays green between them). **Merging this PR shrinks
`KNOWN_UNENROLLED` from 5 to 0 — the ratchet reaches zero and slice 390
closes.**

## Scope

```
internal/catalog/metrics
internal/oscal
internal/policy/pdf
internal/policy/seed
internal/risk/aggrule
```

## Enrolment-form decisions

All five enrol as the **per-leaf recursive form `./internal/<pkg>/...`** — NOT
bare. Each directory is a single leaf package: one `integration_test.go` plus
its production source (`loader.go`+`seed.go`; the oscal `*.go` family;
`render.go`; `seed.go`; `dsl.go`+`engine.go`+`severity.go`+`store.go`) and **no
tracked deeper subpackages** (`find internal/<pkg> -type d` returns only the
package dir itself). The bare-root form (slice 403 api-root / slice 406
`internal/auth`) is reserved for a directory holding only a root `_test.go`
whose subpackages are tracked separately to avoid double-enrolment; that
condition does not hold for any of these five. Each entry was listed in
`KNOWN_UNENROLLED` in bare `internal/<pkg>` form, which the allowlist stores
MINUS the trailing `/...` by design (the script strips `/...` before comparing,
so a bare allowlist line and a `./internal/<pkg>/...` ci.yml line are the same
package). The five new entries were inserted into the **"Run integration
tests"** `go test` invocation ONLY (immediately after
`./internal/exception/...`, before the bare `./internal/api` root which stays
last per the slice-403 form decision); the `errleak-lint` + `duphelper-lint`
`just` steps elsewhere in ci.yml also carry `./internal/...` tokens and were
NOT touched (slice-403 GREP-TRAP precedent honoured).

## Local harness

Stood up a CI-faithful harness: fresh `postgres:16-alpine` container
(`security-atlas-pg-408`, host port 55408), applied
`migrations/bootstrap/01-roles.sql` as the superuser, set
`atlas_app`/`atlas_migrate` passwords, then applied all 66 forward migrations
in order as `atlas_migrate` via plain `psql` (per the memory note that the
Atlas community build panics on apply). 87 tables created. All five suites need
only `DATABASE_URL_APP` (atlas_app role) + `DATABASE_URL` (atlas_migrate /
BYPASSRLS role); `internal/oscal` optionally drives a Python compliance-trestle
bridge subprocess (see below). No MinIO/NATS required for the per-package runs.

## Per-package outcomes

| Package                    | Broke? | Own-suite (int+unit) | excludes action       | Floor                    |
| -------------------------- | ------ | -------------------- | --------------------- | ------------------------ |
| `internal/catalog/metrics` | no     | 94.2%                | n/a (already floored) | **76** unchanged         |
| `internal/oscal`           | no     | 78.9%                | n/a (already floored) | **69** unchanged         |
| `internal/policy/pdf`      | no     | 70.8%                | lifted off `excludes` | **68** = floor(70.8 − 2) |
| `internal/policy/seed`     | no     | 89.5%                | n/a (already floored) | **87** unchanged         |
| `internal/risk/aggrule`    | no     | 74.6%                | n/a (already floored) | **72** unchanged         |

All five GREEN in isolation and in a forced-uncached (`-count=1`) combined
serial `-p 1` run alongside the contaminating `internal/demoseed` neighbour, in
BOTH orderings (demoseed-first and demoseed-last). No FK-wipe contamination
surfaced (the slice-405 lesson) — none of these five issues a global un-scoped
`DELETE FROM <table>`.

### `internal/oscal` — the optional Python bridge (NOT a masked failure)

`TestExport_FrozenPeriodProducesSignedBundle` SKIPs locally with
`oscal-bridge did not become ready … — skipping bridge-dependent test`. This is
the test file's own **decision D2**: the OSCAL SSP/POA&M export pipeline can
talk to a real Python `oscal-bridge` subprocess (compliance-trestle + grpcio),
and when that interpreter/library is unavailable the bridge-dependent test
skips with a clear marker rather than failing. Two reasons this is correct, not
a skip-to-green:

1. It is **pre-existing, documented behaviour** in the suite as authored
   (slice 030), not something this batch introduced.
2. CI provisions **no** Python bridge in the `tests-integration` job (verified:
   no `setup-python` / `uv` / `pip` / `trestle` step between the job header and
   the integration run), so the test skips identically in CI — enrolment does
   not turn a green into a red. The load-bearing Aggregate-side checks,
   including the constitutional invariant-10 "refuse a non-frozen period"
   guard, run with **no bridge at all** and pass.

The genuine-precondition-gap escalation path (spillover + skip-one-test) was
NOT needed: the suite already self-handles the optional dependency cleanly, and
the non-bridge tests give real coverage. No spillover filed for this.

### `internal/catalog/metrics` — slice-386 NULL-encoding path is genuinely tested

HIGH-WATCH per the brief (slice 386 found a `metrics_catalog` NULL-encoding bug
here: `nonNilStrings` must turn an empty `[]string` into `[]` not SQL NULL,
because `metrics_catalog.source_slices` is NOT NULL and pgx encodes a nil
`[]string` as SQL NULL). Confirmed the fix is genuinely exercised, not a
happy-path-only test:

- `seed_test.go` asserts `nonNilStrings(nil)` and `nonNilStrings([]string{})`
  both return **non-nil** (and an independent copy), directly pinning the fix.
- The integration `SeedFromEmbedded` round-trips a real INSERT against the
  NOT-NULL `source_slices` column via `nonNilStrings(m.SourceSlices)`.

No bug to fix. Floor left at the conservative pre-existing 76 (measured 94.2%);
not raised — slice scope is enrolment, and a conservative existing floor is a
valid monotonic ratchet, not a regression.

## Latent exclude-shadow bug — FIXED IN-PLACE (the real find of this batch)

Enrolling `internal/policy/pdf` and lifting it off `excludes` to a floor
surfaced a **pre-existing latent config bug** in the coverage gate:

- `cmd/scripts/coverage-gate/main.go` treats every `excludes` entry as a
  **prefix** (`pkg == p || strings.HasPrefix(pkg, p+"/")`, lines 203–210) and
  **skips any threshold key that matches** (`if isExcluded(key) { continue }`,
  line 238).
- The `excludes` list carried `internal/policy/` — intended (per its
  justification) for the **root** `internal/policy` handler package only. But
  the prefix form silently shadowed **every** `internal/policy/*` subpackage.
- Consequence: the pre-existing `internal/policy/seed: 87` floor was **dead
  config** (silently skipped, never enforced), and the new `internal/policy/pdf:
68` floor would have been dead too — AC-4 ("lift to a real per-package floor")
  would have been cosmetically satisfied but functionally void.

Verified empirically: a `coverage-gate` run over a profile containing
policy/seed data did NOT list `internal/policy/seed` in either the checked or
the failed set — it was skipped by the `internal/policy/` prefix.

**Fix (in-place, correct, not deferred):** removed the blanket
`internal/policy/` exclude (+ its `$exclude_justifications` entry) and gave the
root package its own honest floor `internal/policy: 35` (`floor(37.1 − 2)`,
own-suite measured DB-only). This un-shadows the `seed` (87) and `pdf` (68)
subpackage floors so they are now actually enforced, and keeps the root gated
at its real coverage. This is the same "lift exclude → real floor" move the
slice-401..407 precedents established; I applied it one level up because the
shadow was structural. No spillover filed — the fix is small, correct, and
directly required for AC-4 to be real.

Verification: built a merged unit+integration profile for the policy / catalog
/ oscal / risk family and ran `coverage-gate`. None of `internal/policy`,
`internal/policy/seed`, `internal/policy/pdf`, `internal/catalog/metrics`,
`internal/oscal`, `internal/risk/aggrule` appears in the FAILURES set — they are
all now CHECKED (proving the shadow is gone) and PASS their floors. (The 12
failures in that partial-profile run are unrelated packages that received only
incidental transitive coverage from the limited package set — an artifact of
the partial profile; in CI the full integration list runs and those packages
get their proper coverage. `scripts/check-coverage-excludes.sh` reports OK: all
39 remaining excludes carry a justification, no orphans.)

## Allowlist drained to empty — guard self-handles the empty case

`scripts/audit-integration-enrolment.sh`'s `KNOWN_UNENROLLED` heredoc is now
**empty** (the five entries removed). The guard handles the empty case without
modification:

- `read -r -d '' KNOWN_UNENROLLED <<'WAIVED'` with empty content sets the var
  to the empty string and returns non-zero, absorbed by the existing `|| true`.
- `printf '%s\n' "$KNOWN_UNENROLLED" | sed '/^[[:space:]]*$/d'` strips the
  single blank line → an empty `waived_tmp`.
- `comm`/`cat` over an empty waived set are well-defined; the stale-waiver
  hygiene check (`comm -23 waived tagged`) is empty → no false stale-fire.
- The OK message reports `0 on the slice-345 known-gaps allowlist`.

There is **no** "expected N waived" assertion in the script to update — the
count is computed (`waived_n="$(wc -l < "$waived_tmp")"`), so it naturally reads 0. Confirmed:

- `bash scripts/audit-integration-enrolment.sh` → `OK — 87 tagged package(s);
87 enrolled; 0 on the slice-345 known-gaps allowlist`, exit 0.
- `bash scripts/audit-integration-enrolment_test.sh` → `12 passed, 0 failed`.

## Final slice-390 tally (all eight drain batches)

| Batch | Slice | Packages                                                  | Real find?                                                                       |
| ----- | ----- | --------------------------------------------------------- | -------------------------------------------------------------------------------- |
| pre   | 314   | `internal/api/oauth`                                      | yes — device-flow `postForm` 404 (hidden 5 slices)                               |
| 1     | 401   | oidc / jwtmw / users / period                             | no (benign)                                                                      |
| 2     | 402   | admin-creds surface (5)                                   | yes — real `adminauditlog` product bug + stale tenancy-mw test                   |
| 3     | 403   | adminusers / aggregationrules / api-root / me / decisions | no (benign)                                                                      |
| 4     | 404   | api domain handlers A (5)                                 | no (benign)                                                                      |
| 5     | 405   | api domain handlers B (5)                                 | yes — `ucfcoverage` stale-test + genuine `TRUNCATE … CASCADE` FK-wipe fix        |
| 6     | 406   | auth substrate + keystore (4)                             | yes — stale OIDC nonce-ordering test                                             |
| 7     | 407   | freshness / drift family (4)                              | no (benign)                                                                      |
| 8     | 408   | catalog / oscal / policy / risk tail (5)                  | no test broke; surfaced + fixed the `internal/policy/` exclude-shadow config bug |

**`KNOWN_UNENROLLED` is now EMPTY.** The slice-345 ratchet — authored
2026-05-29 with a 38-package backlog — has reached zero. Every Go package
carrying a `//go:build integration` tag now runs in CI's integration job. The
structural enrolment gap slice 390 set out to close is closed.

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` (no integration test broke this batch).
- `detection_tier_target`: `none`.

The `internal/policy/` exclude-shadow was a **coverage-gate config** defect, not
a runtime/test defect — it caused a floor to be silently un-enforced rather than
a bug to ship. It is outside the unit/integration/playwright/contract test-tier
taxonomy (it is a meta-gate misconfiguration), so it is recorded here in prose
rather than forced into a tier value. Found by reading the gate's
prefix-matching logic while wiring AC-4, fixed in the same PR.

## Files touched

- `.github/workflows/ci.yml` — 5 entries added to the "Run integration tests" list.
- `scripts/audit-integration-enrolment.sh` — `KNOWN_UNENROLLED` drained to empty (+ a note).
- `cmd/scripts/coverage-thresholds.json` — added `internal/policy/pdf: 68` and
  `internal/policy: 35` floors; removed `internal/policy/` and
  `internal/policy/pdf/` from `excludes` and their `$exclude_justifications`.
- `CHANGELOG.md` — Unreleased entry.
- `docs/audit-log/408-integration-drain-batch-8-decisions.md` — this file.
