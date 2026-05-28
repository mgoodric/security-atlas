# 348 — Coverage `excludes` audit

**Slice:** 348 (Cluster E, U-2)
**Date:** 2026-05-28
**Auditor:** slice 348 implementation (engineer-as-collaborator)
**Scope:** read-only categorization of the 61 entries in `cmd/scripts/coverage-thresholds.json` `excludes`
**Disposition:** audit only. Actual coverage lifts are out of scope and become separate slices per the slice-069 monotonic-ratchet contract.

---

## Methodology

The `excludes` list is the Go-side coverage gate's escape hatch — a
package on this list is NOT required to maintain a per-package floor.
Slice 334 U-2 (Medium) flagged the list as "60 entries, the path of
least resistance for landing a slice without a floor". This audit
reviews each entry and categorizes it as:

- **(a) AUTOGEN** — auto-generated code; legitimate, keep
- **(b) CLI** — small CLI script (`main.go`-only), tested in
  integration; legitimate, keep
- **(c) TEST_PRESENT** — package has tests on disk but NOT in the
  coverage profile; almost certainly a CI enrolment gap (slice I-1
  family) or an intentional integration-only measurement. Verify and
  either retire from the excludes list (with the test enrolment) or
  document the reason for the exclusion.
- **(d) NO_TESTS** — package has no tests at all; this is the
  unaudited debt the list grew to hide. Each entry deserves its own
  follow-up coverage-lift slice that writes the missing tests AND
  raises the package off the excludes list.
- **(e) MISSING** — directory not present on disk; stale entry that
  should be removed from `excludes` in a one-line cleanup PR.

The categorization was produced by a discovery script (see slice 348
decisions log D-E1) that walks each excluded package and counts
`*_test.go` vs `*_integration_test.go` files.

---

## Counts

| Category         | Count |
| ---------------- | ----- |
| **AUTOGEN**      | 2     |
| **CLI**          | 2     |
| **TEST_PRESENT** | 27    |
| **NO_TESTS**     | 29    |
| **MISSING**      | 1     |
| **Total**        | 61    |

**Interpretation:** Almost half the list (29 of 61) is unaudited debt
— packages with NO tests at all. Another 27 have tests on disk but
are excluded from coverage measurement, which strongly suggests CI
enrolment gaps (a class of issue slice 345 is filed against).

---

## Categorized list

### (a) AUTOGEN — keep (2)

These are generated code; the floor would measure code the project
doesn't author. Excludes are correct.

| Path               | Generator | Note                                                        |
| ------------------ | --------- | ----------------------------------------------------------- |
| `gen/proto/`       | `protoc`  | gRPC generated Go from `proto/*.proto`                      |
| `internal/db/dbx/` | `sqlc`    | sqlc-generated query code (pinned to v1.31.1 per slice 109) |

**Action:** none. Add a `$comment` field on the `excludes` block in
the thresholds file to memorialize these as legitimate auto-gen.

### (b) CLI — keep (2)

These are `main.go`-only CLI binaries. They're tested end-to-end via
the integration job (`go test -tags=integration -p 1
./internal/...`) but `main.go` covers binary entry, not algorithms.

| Path                          | Note                                                                                                                                         |
| ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `cmd/scripts/coverage-check/` | The coverage-floor verifier (slice 069). Exclusion is the right shape — measuring the gate's own per-line coverage would be circular.        |
| `cmd/scripts/coverage-gate/`  | The same gate, advisory + monotonic-ratchet enforcement. Same circularity argument. The package was added as an exclude in slice 069 itself. |

**Action:** none.

### (c) TEST_PRESENT — investigate (27)

These packages have `*_test.go` or `*_integration_test.go` files on
disk but are excluded from coverage measurement. Two sub-causes are
plausible:

1. **CI enrolment gap.** The integration job's package list
   (`ci.yml` lines 515-630) doesn't enumerate every package with an
   integration_test.go. Slices 279, 283, 284, 287, 288, 290, 293,
   294, 295, 297, 310, 313, 315, 317, 318, 319, 320 retroactively
   enrolled packages that had been silently un-measured. Slice 345
   files the discovery-primitive that closes this gap structurally.
2. **Intentional exclusion.** A small subset of packages may be
   excluded because the tests cover only an HTTP-handler-wrapper that
   delegates to a covered sub-package, so per-package coverage would
   double-count.

| Path                            | Files                | Hypothesis                                                          |
| ------------------------------- | -------------------- | ------------------------------------------------------------------- |
| `internal/api/adminauditlog/`   | integration=5 unit=0 | Likely CI-enrolment gap; 5 integration tests + handler-only package |
| `internal/api/admincreds/`      | integration=1 unit=0 | Likely CI-enrolment gap                                             |
| `internal/api/admindemo/`       | integration=1 unit=1 | CI-enrolment gap                                                    |
| `internal/api/adminsso/`        | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/api/adminusers/`      | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/api/anchors/`         | integration=2 unit=2 | CI-enrolment gap (anchors has substantive test surface)             |
| `internal/api/audit/`           | integration=0 unit=1 | Unit-only; verify whether the wrapper-delegate exemption applies    |
| `internal/api/auth/`            | integration=0 unit=1 | Unit-only; auth-substrate-v2 plumbing — verify exemption rationale  |
| `internal/api/board/`           | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/api/dashboard/`       | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/api/dashboardexport/` | integration=0 unit=1 | Unit-only export handler; verify exemption rationale                |
| `internal/api/decisions/`       | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/api/emptyset/`        | integration=1 unit=0 | CI-enrolment gap (the audit harness from slice 150)                 |
| `internal/api/evidence/`        | integration=0 unit=1 | Unit-only; verify exemption                                         |
| `internal/api/exceptions/`      | integration=0 unit=1 | Unit-only; verify exemption                                         |
| `internal/api/features/`        | integration=1 unit=0 | CI-enrolment gap (slice 320 retroactive enrolment pattern)          |
| `internal/api/freshnessdrift/`  | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/api/me/`              | integration=1 unit=1 | CI-enrolment gap                                                    |
| `internal/api/policies/`        | integration=2 unit=2 | CI-enrolment gap                                                    |
| `internal/api/policyacks/`      | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/api/risks/`           | integration=3 unit=1 | CI-enrolment gap                                                    |
| `internal/api/scfimport/`       | integration=0 unit=1 | Unit-only; verify exemption                                         |
| `internal/api/ucfcoverage/`     | integration=0 unit=1 | Unit-only; verify exemption                                         |
| `internal/auth/sessions/`       | integration=0 unit=1 | Unit-only; sessions primitive — verify exemption                    |
| `internal/auth/users/`          | integration=1 unit=0 | CI-enrolment gap                                                    |
| `internal/policy/`              | integration=2 unit=2 | CI-enrolment gap                                                    |
| `internal/policy/pdf/`          | integration=1 unit=0 | CI-enrolment gap (chromedp render is integration-only)              |

**Action:** Each row deserves a triage pass: (a) verify the
CI-enrolment vs intentional-exemption hypothesis by reading the test
files; (b) if CI-enrolment gap, the package gets enrolled + a hard
floor lifted into `thresholds`; (c) if intentional, the `excludes`
list adds an inline `$comment` field naming the wrapper sub-package.
Most of these are likely slice 345's job once the discovery primitive
lands.

### (d) NO_TESTS — unaudited debt (29)

These packages have zero tests on disk. Each is a coverage-lift slice
candidate.

| Path                              | Estimated severity                                                                                   |
| --------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `catalogs/metrics/`               | Low — likely data files, not code                                                                    |
| `internal/api/admin/`             | Medium — admin service wrapper; verify whether code is generated boilerplate                         |
| `internal/api/aggregationrules/`  | Medium — aggregation rule HTTP surface                                                               |
| `internal/api/anchorseed/`        | Low — slice 006 anchor seeding (one-shot)                                                            |
| `internal/api/artifacts/`         | Medium — artifact API                                                                                |
| `internal/api/auditnotes/`        | Medium — auditor notes API (slice 027 family)                                                        |
| `internal/api/auditperiods/`      | High — audit-period freezing is constitutional invariant 10                                          |
| `internal/api/authctx/`           | High — auth context primitive; security-critical                                                     |
| `internal/api/connectorregistry/` | Medium — connector registry API                                                                      |
| `internal/api/controlstate/`      | High — control state surface                                                                         |
| `internal/api/frameworkscopes/`   | High — FrameworkScope intersection (invariant 5)                                                     |
| `internal/api/idemstore/`         | Medium — idempotency store; load-bearing for Evidence SDK                                            |
| `internal/api/orgunits/`          | Medium — org-unit API                                                                                |
| `internal/api/scopes/`            | High — scope API (invariant 4)                                                                       |
| `internal/api/themes/`            | Low — UI theme API                                                                                   |
| `internal/api/vendors/`           | Medium — vendor risk API                                                                             |
| `internal/api/walkthroughs/`      | Medium — auditor walkthrough API (slice 027 family)                                                  |
| `internal/audit/auditor/`         | Medium — audit subsystem                                                                             |
| `internal/audit/notes/`           | Medium — audit notes                                                                                 |
| `internal/audit/notifications/`   | Medium — audit notifications                                                                         |
| `internal/audit/period/`          | High — audit period invariant                                                                        |
| `internal/auth/apikeystore/`      | High — API key storage; security-critical                                                            |
| `internal/auth/oidc/`             | High — OIDC RP (auth-substrate)                                                                      |
| `internal/drift/`                 | Medium — drift detection                                                                             |
| `internal/evidence/ingest/`       | High — evidence ingestion (invariants 2 + 3) — likely covered via integration but excluded from gate |
| `internal/evidence/streambuf/`    | Medium — stream buffer                                                                               |
| `internal/exception/`             | Medium — exception lifecycle                                                                         |
| `internal/freshness/`             | Medium — freshness primitive                                                                         |
| `internal/freshnessdrift/`        | Medium — freshness drift read model (slice 016)                                                      |

**Action:** Each high-severity entry deserves a dedicated coverage-lift
slice; medium entries can bundle (3-5 packages per slice); low entries
can be deferred or retired with a single broader slice. Total: 29
packages, likely ~10-12 follow-up slices to retire.

### (e) MISSING — stale entry (1)

| Path              | Action                                                                                                |
| ----------------- | ----------------------------------------------------------------------------------------------------- |
| `internal/proto/` | Remove from `excludes` in a one-line cleanup PR (the directory doesn't exist; the entry is leftover). |

---

## Recommendation

Three follow-up tracks:

1. **Stale cleanup (immediate).** Remove `internal/proto/` from
   `excludes`. One-line PR. Not slice 348's job; could ride on slice
   345 or a free polish slot.
2. **TEST_PRESENT enrolment (high leverage).** Slice 345's discovery
   primitive will surface most of the 27 CI-enrolment gaps. After
   slice 345 lands, re-audit this category — many entries will retire
   to per-package floors automatically.
3. **NO_TESTS debt (slow drain).** The 29 zero-test packages each
   need a coverage-lift slice. Estimated 10-12 slices over the next 2
   quarters. Prioritize HIGH-severity entries: `auditperiods/`,
   `authctx/`, `controlstate/`, `frameworkscopes/`, `scopes/`,
   `period/`, `apikeystore/`, `oidc/`, `evidence/ingest/` (the
   constitutional invariant load-bearers).

**Revisit cadence:** quarterly. Track `len(excludes)` as a slice-069
ratchet metric — the list should monotonically shrink, never grow.
A new exclude landing is a code smell that warrants explicit
justification in the PR.

---

## Cross-references

- **Slice 069** — the coverage ratchet contract. The `excludes`
  field is the gate's escape hatch; this audit measures the cost of
  that escape.
- **Slice 334 U-2 (Medium)** — the framework audit finding this
  audit operationalizes.
- **Slice 345** — the discovery primitive for the integration job's
  package list; expected to retire most TEST_PRESENT entries.
- **Slice 347** — TS-side coverage ratchet; the Go side now has 110+
  per-file floors; the TS side has 107 per-file floors (per slice
  347's landing). The two halves of the product now have symmetric
  measurement; this audit confirms the Go-side ratchet's escape
  hatch is the load-bearing concern.
