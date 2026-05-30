# Integration job — enrolment history

This document holds the slice-by-slice enrolment history for the
`tests-integration` job in `.github/workflows/ci.yml`. The job runs the
Go integration suite (build tag `integration`) against a real Postgres,
NATS JetStream, and MinIO; the package list inside its `go test` invocation
is the load-bearing field. Each time a new package is added to that list,
a slice records why — typically "the package shipped an
`integration_test.go` in an earlier slice that was never enrolled in the
CI integration job, so its merged coverage was unit-only".

This is the **retroactive-enrolment pattern**: most coverage lifts in the
v2 round-3 audit campaign were not "write new tests" — they were "enrol
already-shipped tests into the gate". The narrative for each enrolment
slice is preserved below so a future contributor reading the yaml can
understand the shape of those decisions without having to git-blame each
package's line in the package list.

The yaml itself stays structural: the `tests-integration` job documents
its **live** behavior (service containers, environment variables, the
`-p 1` race rationale, the merged-coverage gate architecture, the
TRUNCATE-before-down-migrations rationale). Slice 346 moved the
historical commentary here as a JUDGMENT-slice spillover from slice
334's framework audit (finding I-2). See
`docs/audit-log/346-ci-yaml-history-extraction-decisions.md`.

## At-a-glance chronology

| Slice | Package(s) enrolled                                                                             | Coverage delta (merged)                                                                                     | Pattern                                              |
| ----- | ----------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| 279   | `internal/frameworkscope`, `internal/risk`, `internal/risk/aggrule`                             | frameworkscope 21.8% → 77.8%; risk 36.1% → 73.4%; risk/aggrule 19.1% → 74.6%                                | enrolment-only (no new tests)                        |
| 283   | `internal/board`                                                                                | 23.7% → 81.6%                                                                                               | new in-package integration test + enrolment          |
| 284   | `internal/scope`                                                                                | 35.3% → 87.6%                                                                                               | enrolment of slice-017 suite; bit-rot fix-up         |
| 287   | `internal/vendor`                                                                               | 10.1% → 87.2%                                                                                               | enrolment + new Get/Delete/RLS tests + unit suite    |
| 288   | `internal/audit/walkthrough`                                                                    | 6.0% → 79.9%                                                                                                | enrolment of slice-027 suite + new export_test.go    |
| 290   | `internal/api/controldetail`                                                                    | 29.3% → 92.7%                                                                                               | enrolment of slice-064 suite + new helpers_test.go   |
| 293   | `internal/api/metrics`                                                                          | 0.4% → 87.3%                                                                                                | new integration_test.go + new unit suite + enrolment |
| 294   | `internal/metrics/eval`                                                                         | 33.0% → 78.4%                                                                                               | new integration_test.go + enrolment                  |
| 295   | `internal/metrics/scheduler`                                                                    | 0.0% → 78.4%                                                                                                | new integration_test.go + new unit suite + enrolment |
| 297   | `internal/policy/...` tree                                                                      | internal/policy/seed 33.3% → 89.5%                                                                          | new seed_integration_test.go + tree-level enrolment  |
| 310   | `internal/api/soc2import`                                                                       | 25.2% → 77.4%                                                                                               | enrolment of slice-007 suite + new helpers_test.go   |
| 313   | `internal/api/adminauditperiods`, `adminsuperadmins`, `admintenants`, `adminvendors`, `tenants` | each above the AC-2 70% merged-coverage floor (parent slices 139 / 142 / 143 / 139 / 144)                   | enrolment + new helpers_test.go suites               |
| 315   | `internal/auth/oauthclient`, `oauthcode`, `revocation`, `userprefs`                             | each above 70% merged                                                                                       | enrolment + new integration_test.go for 3 of 4       |
| 317   | `internal/api/mcpwriteproposals`, `internal/mcp/writeproposals`                                 | internal/mcp/writeproposals 1.8% → 77.5%; internal/api/mcpwriteproposals 0.9% → 84.3%                       | enrolment + new unit helpers/appliers/handlers tests |
| 318   | `internal/audit` (umbrella), `internal/audit/sink`, `internal/audit/unifiedlog`                 | each above 70% merged (umbrella was 0.4%; sink 67.3%; unifiedlog had no in-package integration test at all) | enrolment of slice-026 + slice-126 + new unifiedlog  |
| 319   | `internal/questionnaire`                                                                        | 26.5% → 79.3%                                                                                               | new in-package integration + pdf + helpers tests     |
| 320   | `internal/demoseed`                                                                             | 4.4% → 83.1%                                                                                                | enrolment of slice-205 suite + new helpers/seeder    |

## Preserved commentary (verbatim from prior yaml)

Each block below is the prose that previously lived inline in the
`tests-integration` job's `Run integration tests` step.

### Slice 279

Extended to include `internal/frameworkscope`, `internal/risk`, and
`internal/risk/aggrule` — these had `integration_test.go` files but were
never enrolled in the CI integration job, so their coverage was unit-only.
Adding them moves frameworkscope from 21.8% → 77.8% merged, risk from
36.1% → 73.4% merged, and risk/aggrule from 19.1% → 74.6% merged (no new
tests required).

### Slice 287

Extended to include `internal/vendor` for the same reason — the package
shipped an `integration_test.go` in slice 024 but was never enrolled
here. Adding it (with new Get + Delete + RLS-isolation tests + a unit
suite for the pure-Go helpers) moves `internal/vendor` from 10.1% →
87.2% merged.

### Slice 284

Same pattern for `internal/scope` — it had a complete `integration_test.go`
suite (slice 017) that was never enrolled in CI, so the merged audit
measured 35.3%. Enrolling lifts `internal/scope` to 87.6% merged. Three
pre-existing test inserts gained a `bundle_id` column (NOT-NULL column
added by a later slice; the integration suite had silently bit-rotted
while it was out of CI).

### Slice 297

Extended to include `internal/policy/...` — the slice added a new
`seed_integration_test.go` covering the DB-touching `Seed()` +
`SQLAnchorResolver.Resolve()` paths in `internal/policy/seed` (the
parent `internal/policy` also ships an `integration_test.go` from slice
022, which was previously exercised only outside the CI gate).
Enrolling the entire `./internal/policy/...` tree lifts
`internal/policy/seed` from 33.3% → 89.5% merged.

### Slice 283

Extended to include `internal/board` — the slice added a new
`integration_test.go` covering the slice-031 board brief Store +
Generator and the slice-032 quarterly pack PackStore + PackGenerator
(Insert / Get / List / UpdateSection / Publish / ListFailingEvaluations)
against a real Postgres so RLS, append-only ledger enforcement, and the
draft-only mutation gate are exercised end-to-end. Enrolling the package
lifts `internal/board` from 23.7% → 81.6% merged.

### Slice 290

Extended to include `internal/api/controldetail` — the package shipped a
932-line `integration_test.go` in slice 064 but was never enrolled in
the CI integration job, so the merged audit measured 29.3%. Enrolling
it (alongside the new `helpers_test.go` this slice adds for the pure-Go
split / cursor / wire-conversion helpers) lifts
`internal/api/controldetail` to 92.7% merged.

### Slice 288

Extended to include `internal/audit/walkthrough` — the package shipped a
571-line `integration_test.go` in slice 027 but was never enrolled in
the CI integration job, so the merged audit measured 6.0%. Enrolling it
(alongside the new `export_test.go` this slice adds for the pure-Go PDF
HTML + markdown rendering helpers) lifts `internal/audit/walkthrough`
to 79.9% merged. Only `./internal/audit/walkthrough/...` is added — NOT
the `./internal/audit/...` umbrella — so adjacent walkthrough-unrelated
audit subpackages (`auditor/`, `notes/`, `period/`, etc.) are not
picked up by this slice's enrollment.

### Slice 295

Extended to include `internal/metrics/scheduler` — the slice adds a new
`integration_test.go` covering `SweepOnce`'s success branches (per-tenant
transaction lifecycle, evaluator try/log/continue, `InsertMetricObservation`
through the RLS-bound app role) and `Run`'s inline-sweep + `ctx.Done`
path. The unit tests cover `New` / `Run` cancellation / `encodeDimensions` /
`discardWriter` / `SweepReport` / `SweepOnce`'s list-tenants-error wrap.
Together they move `internal/metrics/scheduler` from 0.0% → 78.4%
merged.

### Slice 293

Extended to include `internal/api/metrics` — the slice adds a new
`integration_test.go` covering the post-auth DB-touching branches
(`ListCatalog` level/category filters, `GetCatalog` parents/children,
`GetCascade` default-level + truncation header, `CreateInput` happy +
wrong-strategy 409 + not-found 404, `ListObservations` through the
manual-input replicate trigger, `Target` upsert insert→update flow +
every accepted direction). The unit tests cover the pre-DB branches
(auth + admin + URL-param + JSON body + direction + UUID parsing) and
every pure helper (`numericPtr`, `numericString`, `numericStringMaybe`,
`uuidString`, the four wire mappers, `writeJSON`, `writeError`).
Together they move `internal/api/metrics` from 0.4% → 87.3% merged.

### Slice 294

Extended to include `internal/metrics/eval` — the slice adds a new
`integration_test.go` covering each starter evaluator's `Compute()`
against real Postgres (empty + populated branches for the 6 evaluators
that work in v1, plus the wrapped-error branch for
`audit_readiness_score` and `policy_attestation_rate` — both reference
v1 schema columns/tables that don't exist yet). The unit tests already
cover `Name()` + the registry methods. Together they move
`internal/metrics/eval` from 33.0% → 78.4% merged.

### Slice 310

Extended to include `internal/api/soc2import` — the package shipped a
287-line `integration_test.go` in slice 007 but was never enrolled in
the CI integration job, so the merged audit measured the same 25.2% as
the unit-only run (the integration tests against real Postgres exercise
`Import` + `importIntoTx` — every requirement / edge / idempotency /
source_attribution / STRM-distribution branch — but none of that
contributed to coverage). Enrolling it (alongside the new
`helpers_test.go` this slice adds for the pure-Go helpers
`uuidFromString` / `uuidString` / `parseDate` / `edgeContentEqual` and
the `Import` BeginTx-error branch via a stub `pgxBeginner`) lifts
`internal/api/soc2import` to 77.4% merged. Same playbook as slices
290 / 291 / 293 — enrolment is the load-bearing move.

### Slice 315

Extended to include the four auth-substrate-v2 small packages
(`oauthclient` + `oauthcode` + `revocation` + `userprefs`) flagged by
the round-3 audit (`docs/coverage-audit-2026-05-round-3.md`). Each was
<100 stmts and below 70% merged because none was enrolled in the
integration job. New `integration_test.go` suites added for
`oauthclient`, `oauthcode`, and `userprefs` (`revocation` already
shipped a comprehensive one in slice 190). Same playbook as slices
290 / 297 / 310 — enrolment is the load-bearing move.

### Slice 317

Extended to include the two MCP write-proposals packages
(`internal/api/mcpwriteproposals` + `internal/mcp/writeproposals`)
flagged by the round-3 audit. Both already shipped comprehensive
`integration_test.go` suites (HTTP handler + store layer driving HITL
approval flow against real Postgres + RLS + the schema-level
`ai_assist` invariant CHECK) but neither was enrolled in the CI
integration job. New unit `helpers_test.go` + `appliers_test.go` +
`handlers_test.go` added for the pure-Go pre-DB validation branches
(`writeCreateErr` / `writeTransitionErr` / `writeServerErr`, the four
applier parse + required-field branches,
`stateToEvidenceResult` + the `nullableString`/`Subject` helpers, plus
the Create/Confirm pre-tx rejections). Together with enrolment they
lift `internal/mcp/writeproposals` from 1.8% → 77.5% merged and
`internal/api/mcpwriteproposals` from 0.9% → 84.3% merged. Same
playbook as slices 290 / 297 / 310 / 315 — enrolment is the
load-bearing move.

### Slice 319

Extended to include `internal/questionnaire` — the questionnaire engine
(Excel parser + AnswerLibrary RLS lookup + PDF render helpers + Store
CRUD over four tables). Was at 26.5% merged because none of its Store
paths had an in-package `integration_test.go` (the existing
`internal/api/questionnaires` integration tests exercise the Store
transitively but the coverage attributes to the HTTP handler package,
not the engine). The new in-package `integration_test.go` drives Store
CRUD + the RLS isolation invariant directly. Together with the new
`pdf_test.go` (pure-Go renderer helpers) + `store_helpers_test.go`
(conversion helpers) the engine moves from 26.5% → 79.3% merged. Same
playbook as slices 290 / 297 / 310 / 315 — enrolment is the
load-bearing move.

### Slice 313

Extended to include the five admin HTTP handler packages
(`adminauditperiods` + `adminsuperadmins` + `admintenants` +
`adminvendors` + `tenants`) flagged by the round-3 audit. Each had a
comprehensive `integration_test.go` shipped by its parent slice
(139 / 142 / 143 / 139 / 144 respectively) that was never enrolled in
the integration job, so the merged audit measured 0.5-7.7% per
package. Enrolling them (plus new `helpers_test.go` suites for
`adminauditperiods` + `adminvendors` + `admintenants` covering pure-Go
pre-DB branches: format parsing, role gating, column projection, email
masking, `writeError`, `exportLimiter`, advisory-key derivation) lifts
each package above the AC-2 70% merged-coverage floor. Same playbook
as slices 290 / 297 / 310 / 315.

### Slice 318

Extended to include the three audit-log family packages
(`internal/audit` umbrella + `internal/audit/sink` +
`internal/audit/unifiedlog`) flagged by the round-3 audit. The
umbrella shipped a 660-line `integration_test.go` in slice 026 but was
never enrolled, leaving merged coverage at 0.4%; the sink package
shipped a 330-line `integration_test.go` in slice 126 covering AC-7
(100-record happy path) + AC-8 (10001-record backpressure fallback)
but the package was also unenrolled, leaving merged at 67.3%
(unit-only); the unifiedlog package had no in-package integration
test at all. A new unifiedlog `integration_test.go` (this slice)
drives `Query` against real Postgres + RLS for the canonical-shape,
tenant-isolation, kind-filter, empty-range, and actor-filter
branches. Together with `helpers_test.go` (umbrella pure-Go predicate
canonicalization + validation guards) and `helpers_test.go` (sink
`Default` reader + `Shutdown` ctx-cancel + no-op discard paths) and
`joinkinds_test.go` (unifiedlog CSV serializer + `AllKinds` order
contract), enrolment lifts the three packages above the AC-1 70%
merged-coverage floor. Same playbook as slices 290 / 297 / 310 / 315.

### Slice 320

Extended to include `internal/demoseed` — the slice 205 demo dataset
seeder. The package shipped a 341-line `integration_test.go` (slice 205) that drove `TestApply_Happy` / `_Idempotent` / `_RefusesPopulated`
/ `_CrossTenantIsolation` / `_Scale` / `_RejectsInvalidSlug` against
real Postgres but was never enrolled in the CI integration job, so the
merged audit measured 4.4% (unit-only — only the password generator +
names constants were exercised). Enrolling it (alongside new
`helpers_test.go` for the pure-Go writer helpers — `withTenant` /
`currentTenantOf` / `nullableUUID` / `nonZeroOrSelf` /
`nonZeroOrTenant` / `periodStatus` / `frozenHashOrNil` /
`frozenByOrNil` / `sha256Of` / `capitalize` / `kindToConnector` /
`riskScoreJSON` / `fictionalUserEmail` / `buildEvidencePayload`
default branch + `seeder_test.go` for `NewSeeder` / `WithClock` /
`validateSlug` / `pgIsUndefinedTable` / `applyScale` /
`hashCanonicalJSON` / `hexString`) lifts `internal/demoseed` from
4.4% → 83.1% merged. Same playbook as slices 290 / 297 / 310 / 315 —
enrolment is the load-bearing move, with unit-tests paying off the
pure-Go branches the integration test cannot reach (error-path
constructors, sentinel-error detection, scale-clamp edge cases).

## How to extend this document

When a future slice enrols another package in the integration job:

1. Add the row to the **At-a-glance chronology** table.
2. Add a new `### Slice NNN` section under **Preserved commentary** with
   the same prose shape as the existing entries (package name +
   why-not-enrolled-before + before% → after% + same-playbook reference).
3. In the yaml itself, add the package to the `go test` package list
   without a new inline comment — the sidecar is the system of record
   for enrolment narrative.
