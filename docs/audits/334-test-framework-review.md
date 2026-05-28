# 334 — Test framework review report

**Slice:** 334
**Date:** 2026-05-27
**Auditor:** `voltagent-qa-sec:test-automator` persona (instance run)
**Scope:** read-only framework audit; no test file or CI config modified

---

## Methodology

This audit applies the `voltagent-qa-sec:test-automator` persona at
`~/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/test-automator.md`
to the four enforced test frameworks documented in `CLAUDE.md` "Testing discipline":

1. Go unit (`go test ./...`)
2. Go integration (`go test -tags=integration -p 1 ./internal/...`)
3. Frontend vitest (`web/`)
4. Frontend Playwright e2e (`web/e2e/` + `web/e2e-audit/`)

The persona's "build a framework" lens is adapted to the project's
"audit an existing framework" reality: the persona's framework-design
sections (architecture, pattern catalogue, tool selection) become rubrics
for the audit, not implementation targets.

### Severity mapping

- **High** = blocks framework growth, hides a class of bug, or contradicts a constitutional invariant
- **Medium** = drag on developer velocity or sustainability (drift catches up later)
- **Low** = style nit; bundle into a polish round

### Audit surface (bounded)

| Framework      | Files surveyed                                                                                                                                                       |
| -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go unit        | `internal/policy/pdf/*_test.go`, `internal/board/*_test.go`, `internal/eval/*_test.go`, `cmd/scripts/coverage-thresholds.json`, `.github/workflows/ci.yml` L100-200  |
| Go integration | `internal/db/integration_test.go`, `internal/policy/pdf/render_integration_test.go`, `internal/api/oauth/*_integration_test.go`, `.github/workflows/ci.yml` L191-630 |
| Vitest         | `web/vitest.config.ts`, `web/lib/api.test.ts`, `web/lib/api/bff.test.ts`, `web/app/api/install-state/route.test.ts`                                                  |
| Playwright e2e | `web/playwright.config.ts`, `web/e2e/README.md`, `web/e2e/admin-demo.spec.ts`, `web/e2e/fixtures.ts`, `web/e2e-audit/README.md`                                      |

The bounded sample surfaced more than three substantive findings, so the
audit was not expanded per the bounded-scope rule. The full file
inventory (hundreds of test files) is out of scope for this slice.

### Out of scope (deferred)

- The remaining ~120 Go `*_test.go` files outside the survey set
- The remaining ~70 Go `*_integration_test.go` files outside the survey set
- The remaining ~40 vitest `*.test.ts` files
- The remaining ~40 Playwright `*.spec.ts` files
- `connectors/**/*_test.go` — the connector test pattern is its own surface (slice 004 locked it; worth a future audit)
- `oscal-bridge/**` — Python tests, different runner, not part of the four-surface gate
- Mutation testing (`gremlins`, `stryker`) — that is a strategy question owned by slice 333

A future audit slice can apply the same methodology to a different
bounded surface; the cap on this slice is 1.5d.

---

## Findings

### Surface 1: Go unit

| #   | Severity | File / location                                                                                       | Category               | Detail                                                                                                                                                                                                                                                                                                                                          |
| --- | -------- | ----------------------------------------------------------------------------------------------------- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| U-1 | Medium   | `internal/eval/state_test.go` (whole file, ~290 LOC), `internal/api/oauth/token_test.go` (whole file) | anti-pattern           | Zero `t.Parallel()` calls in pure-unit test files that have no shared mutable state. Sibling `internal/eval/helpers_test.go` and `internal/board/pdf_html_test.go` use `t.Parallel()` on every test (18 + 14 calls respectively). The two patterns coexist in the same package directory — `t.Parallel()` adoption is by-author, not by-policy. |
| U-2 | Medium   | `cmd/scripts/coverage-thresholds.json` `excludes` list (60 entries)                                   | sustainability-concern | 60 packages are excluded from the coverage gate, including high-value packages like `internal/policy/pdf/` (the chromedp render path), `internal/freshnessdrift/`, `internal/evidence/ingest/`, and 23 `internal/api/*` handlers. The exclude list has become the path of least resistance for landing a slice without a floor.                 |
| U-3 | Low      | `internal/board/pdf_html_test.go` lines 56-148                                                        | sustainability-concern | HTML-rendering tests use `strings.Contains(got, "Top risks aging")` to assert HTML shape. Fragile to whitespace / refactor / template-engine changes. A `testdata/*.golden.html` pattern with `go test -update` would catch regressions more cheaply once the template stabilizes. Acceptable today; flag for future maintenance.               |
| U-4 | Low      | `internal/policy/pdf/render_integration_test.go` line 121                                             | convention-drift       | The `TestRender_ProducesRealPDF_TenIterations` test runs 5 iterations, not 10, with the rationale documented in-line (timeout math). Name-to-behavior mismatch is permanent friction. Either rename to `_FiveIterations` or split the timeout work to allow 10 honest iterations.                                                               |
| U-5 | Low      | `internal/eval/state_test.go` line 5 vs `internal/eval/integration_test.go` build tag                 | convention-drift       | Unit tests use `package eval` (internal access); integration tests use `package eval_test` (external access via build tag). Pattern is consistent across the audited packages but unwritten. Document the convention in `Plans/canvas/09-tech-stack.md` or `CONTRIBUTING.md`.                                                                   |

**Density:** 0 high · 2 medium · 3 low = 5 findings; bundle most into a polish round, U-2 (excludes growth) is the load-bearing maintenance concern.

### Surface 2: Go integration

| #   | Severity | File / location                                                        | Category               | Detail                                                                                                                                                                                                                                                                                                                                                                                                             |
| --- | -------- | ---------------------------------------------------------------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| I-1 | High     | `.github/workflows/ci.yml` lines 515-568 (integration package list)    | sustainability-concern | The integration job's package list is hand-maintained — 47 explicit `./internal/<pkg>/...` entries. Adding a new integration-tested package requires a CI edit (Slices 279, 283, 284, 287, 288, 290, 293, 294, 295, 297, 310, 313, 315, 317, 318, 319, 320 all carry this enrolment work). The list silently bit-rots: an `integration_test.go` ships, the package is forgotten, the merged coverage is unit-only. |
| I-2 | High     | `.github/workflows/ci.yml` lines 142-628 (test job structure)          | framework-config       | The CI yaml is 2488 lines with extensive inline slice-by-slice commentary. The `tests-integration` job alone has ~150 lines of inline historical narrative. Maintenance shape: a new contributor reading the job has to mentally filter ~70% of the content. The history belongs in `git log` or a sidecar doc, not the job body.                                                                                  |
| I-3 | Medium   | `.github/workflows/ci.yml` line 516 (`-p 1` rationale)                 | framework-config       | `-p 1` is comment-justified as "shared rows in `evidence_kind_schemas` and `evidence_records`" — accurate today, but the comment conflates two different things: shared schema-registry seed rows (true conflict) and tenant-isolated RLS data (no conflict). See dedicated `-p 1` review section below.                                                                                                           |
| I-4 | Medium   | `internal/db/integration_test.go` lines 42-72 (`TestMain`)             | convention-drift       | `TestMain` is duplicated across every integration test package — `appPool, adminPool` setup is 30+ LOC each. A shared `internal/testpgx/` helper would eliminate the duplication. Acceptable today (the helper would need its own RLS-aware abstractions); flag for tracking.                                                                                                                                      |
| I-5 | Low      | `internal/db/integration_test.go` line 31 (Postgres SQLSTATE constant) | convention-drift       | `pgErrForeignKeyViolation = "23503"` is redeclared per-package. A shared `internal/testpgx/sqlstates.go` would centralize the canonical list (FK violation, not-null violation, unique violation, etc.). Tracking concern.                                                                                                                                                                                         |

**Density:** 2 high · 2 medium · 1 low = 5 findings; both high-severity items concern the CI integration job's maintenance shape.

### Surface 3: Frontend vitest

| #   | Severity | File / location                                                                 | Category               | Detail                                                                                                                                                                                                                                                                                                                                                                                                                    |
| --- | -------- | ------------------------------------------------------------------------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| V-1 | High     | `.github/workflows/ci.yml` `frontend-vitest` job + `web/vitest.config.ts`       | framework-config       | Vitest has **no coverage ratchet**. Slice 069 shipped the job as "informational, follow-up will raise the bar" — ~250 slices later that follow-up has not landed. The Go side has `cmd/scripts/coverage-gate` + `coverage-thresholds.json` enforcing a monotonic ratchet; the TS side measures coverage and uploads it to an artifact that nothing consumes. Measurement asymmetry between the two halves of the product. |
| V-2 | Medium   | `web/vitest.config.ts` lines 49-89 (`include` array)                            | sustainability-concern | The `include` array is hand-curated, 11 entries with escape-bracket hacks for route-group directories (`app/[(]authed[)]/`). Each new colocated-test directory requires an edit. A two-line glob (`**/*.test.ts`) plus an `exclude` for non-test paths would scale better.                                                                                                                                                |
| V-3 | Medium   | `web/app/api/install-state/route.test.ts` lines 19-35 + bff.test.ts lines 22-45 | anti-pattern           | Every vitest file that imports a Next.js route handler re-declares the `NextResponse` mock (~15 LOC per file). Across the ~40 vitest files this is ~600 LOC of duplicated mock plumbing. A shared `web/lib/test-utils/next-mocks.ts` would centralize it.                                                                                                                                                                 |
| V-4 | Low      | `web/lib/api/bff.test.ts` line 69 (`"test-bearer-token"`)                       | convention-drift       | Test-bearer literals are scattered (`"test-bearer-token"`, `"test-bearer-local"`, `"test-bearer-e2e"`). Centralize in a `web/lib/test-utils/test-tokens.ts` so the GitGuardian neutral-token policy has one place to enforce and so a future migration (slice 197 / 201 was painful) has one edit point.                                                                                                                  |
| V-5 | Low      | `web/vitest.config.ts` line 43 (`environment: "node"`)                          | convention-drift       | All vitest tests run under `node` env per slice 069 P0-A3 (no JSX, no DOM). Reasonable today, but two component-level helpers tests now live under `components/**/*.test.ts`. If component-level testing surface grows, the env decision needs revisiting — flag for tracking.                                                                                                                                            |

**Density:** 1 high · 2 medium · 2 low = 5 findings; V-1 is the load-bearing strategy gap.

### Surface 4: Frontend Playwright e2e

| #   | Severity | File / location                                                | Category               | Detail                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| --- | -------- | -------------------------------------------------------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P-1 | Medium   | `web/e2e/*.spec.ts` (57 `route.fulfill` calls across 46 specs) | anti-pattern           | The `/e2e/` suite is structurally a hybrid: real Next.js process, mocked upstream atlas API surface. The spec name suggests "end-to-end" but the upstream is mocked in nearly every assertion. This is a strategy-level call (cross-ref slice 333) but framework-visible: a regression in the real BFF→atlas wire shape is NOT caught by the e2e suite. The `e2e-audit/` slice-178 harness is the closest thing to true e2e and it is **informational only**. |
| P-2 | Medium   | `web/e2e/` (no Page Object Model adoption across 46 specs)     | convention-drift       | Specs hit testids directly (`page.getByTestId("demo-seed-button")`). With 46 specs and ~3 specs per route, a renamed testid is an N-file refactor. A thin `web/e2e/pages/<route>.ts` Page Object layer per route would let testid renames be one-file edits. Strategy-level call; not a hard anti-pattern at this size.                                                                                                                                       |
| P-3 | Medium   | `web/playwright.config.ts` line 30 (`fullyParallel: false`)    | framework-config       | Spec-file parallelism is disabled. Comment cites "sign-in race vs network mocks", which made sense pre-slice-201 when the bearer was a static literal. Post-201 each worker has its own JWT and per-test `page.route` mocks are scoped to the page's BrowserContext. The constraint may be relaxable; an experiment with `fullyParallel: true` could halve CI wall-clock.                                                                                     |
| P-4 | Medium   | `web/e2e/` (8 files carry `test.skip`)                         | sustainability-concern | 8 spec files have `test.skip` quarantines (`audits-create`, `auth-open-redirect`, `bff-cookie-production-build`, `logo-render-production-build`, `questionnaires`, `risks-create-control-link`, `risks-create`, and the resolved-but-comment-laden `control-detail-tabs`). Some are legitimate env-gates; some are real test-debt. Need a triage pass — every `test.skip` should reference an open spillover slice or an env-gate justification.              |
| P-5 | Low      | `web/playwright.config.ts` lines 46-51 (chromium-only project) | sustainability-concern | Slice 069 deferred firefox + webkit. v1 ship has shipped. The cost-benefit of adding the other two browsers has not been re-evaluated. Not urgent (the BFF is the surface most likely to have a browser-specific bug, and the BFF is `node` runtime), but worth a check-in.                                                                                                                                                                                   |

**Density:** 0 high · 4 medium · 1 low = 5 findings; the cluster around hybrid-mocked-e2e (P-1) and parallelism (P-3) is where the strategy debate lives.

---

## `-p 1` rationale review (AC-8 of the slice doc + task-instruction request)

Slice 334's slice doc explicitly requires an examination of whether
`-p 1` in the integration job is still load-bearing. The full
recommendation:

**Keep `-p 1`. Do not relax it in the current shape.**

### Why it is load-bearing

The CI yaml comment at line 301-307 names three packages that share
rows in shared catalog tables: `db`, `schemaregistry`, `evidence/ingest`.
Reading the seed paths, the actual concrete collisions are:

- **`evidence_kind_schemas` rows**: the integration tests in
  `schemaregistry` and `evidence/ingest` both INSERT canonical
  `evidence_kind` rows during setup. Without `-p 1`, two test binaries
  race on `INSERT ... ON CONFLICT DO NOTHING` against the same
  `(kind, version)` primary key — the conflict is harmless on a single
  binary but surfaces as "duplicate-key violates unique constraint" when
  two test binaries race against the same Postgres instance.
- **`scf_anchors` seed rows**: slice 006 seeds the SCF catalog. Several
  downstream packages INSERT their own anchor rows on top. Cross-binary
  ordering of these INSERTs is non-deterministic without `-p 1`.
- **Append-only ledger rows** in `evidence_records`: slice 003's
  contract is sequential ingestion. Multiple binaries pushing to the
  same `(tenant_id, kind, idempotency_key)` triple-collide on the
  unique index that protects the append-only contract.

### Why `-p 1` is NOT primarily about RLS

A common misreading (visible in the slice 334 task instructions) is
that `-p 1` is about RLS-test isolation. It is not. The RLS pattern
(`internal/tenancy/apply.go`: `SET LOCAL app.current_tenant`) is
TRANSACTION-scoped, not connection-scoped or session-scoped. RLS
isolation works perfectly under parallel package execution because each
test runs its work inside its own `BEGIN; SET LOCAL ...; ... ROLLBACK;`
block. Two parallel binaries each opening their own transaction with
their own tenant GUC cannot leak across.

What `-p 1` protects is the **shared seed and append-only ledger rows
that live at the platform layer, not the tenant layer**. The collision
isn't an RLS bypass; it's a unique-constraint race on rows the platform
seeds for all tenants.

### Where the rationale comment could be sharpened

The CI yaml comment names the symptom ("duplicate-key + missing-relation
errors") but elides the root cause. A 4-line addition would:

1. Name the colliding tables explicitly (`evidence_kind_schemas`, `scf_anchors`, `evidence_records`)
2. Clarify that RLS-isolated test data is NOT what `-p 1` protects
3. Reference this audit's `-p 1` review section
4. Name the v3 path forward (split integration job into shared-write vs tenant-write phases)

That comment edit is a candidate addition to the polish-round-1 slice.

### Future relaxation path (v3, NOT this slice)

If wall-clock pressure surfaces, the path is **not** to remove `-p 1`.
The path is to split the integration job:

- **Phase A (serial, `-p 1`):** packages that seed shared catalog rows
  (`db`, `schemaregistry`, `evidence/ingest`, `catalog`)
- **Phase B (parallel, `-p N`):** packages whose writes are all
  tenant-scoped (`controls`, `risks`, `evidence/records` post-seed,
  `policies`, `audit`, `vendor`)

Splitting requires a per-package classification audit (which is
mechanical from the test code) + a CI yaml change. It is a v3 follow-on,
not a v1 fix, and only worth doing when the integration job's wall-clock
becomes a merge-velocity problem. Today it does not.

---

## Cross-framework observations

Three themes span 2+ surfaces:

### Theme 1 — Measurement asymmetry between Go and TS

The Go side has a monotonic coverage ratchet (`cmd/scripts/coverage-gate`,
`coverage-thresholds.json`); the TS side has measurement that nobody
consumes. The asymmetry is visible to a contributor in two ways:

- A new Go package without a floor triggers an explicit "add it to
  `coverage-thresholds.json`" PR comment from the gate.
- A new TS module with zero tests merges with no signal.

This is finding V-1, surfaced again as a cross-framework concern. The fix
shape is a TS-side ratchet (vitest's `coverage.thresholds` per-file map
exists; populate it).

### Theme 2 — CI yaml as the single point of test-enrolment friction

The Go integration job enrols packages by editing `ci.yml` lines 515-568.
The vitest config enrols colocated test paths by editing
`vitest.config.ts` `include` array. The Playwright config has no
enrolment friction (it walks the directory) — which is the right shape.

The two enrolment lists are silent: a contributor who ships an
`integration_test.go` but forgets the CI edit has a green CI run + a
silently-unmeasured package. Slices 279, 283, 284, 287, 288, 290, 293,
294, 295, 297, 310, 313, 315, 317, 318, 319, 320 all retroactively
enrolled previously-skipped packages. The pattern is the load-bearing
finding (I-1).

Fix shape: a CI step that grep's for `//go:build integration` build tags
and asserts every such package is named in the `go test` package list.
A 30-line shell script would close the gap.

### Theme 3 — Test-fixture / setup duplication across surfaces

Three layers each duplicate setup code:

- Go integration: every `TestMain` re-implements `appPool, adminPool`
  setup (I-4)
- vitest: every route test re-declares the `NextResponse` mock (V-3)
- Playwright: every spec re-implements the `page.route` mock for `/api/admin/demo/status` (P-1 cluster)

None of these are a high-severity bug, but together they say: the
testing-helper layer is under-developed. A `web/lib/test-utils/`,
`internal/testpgx/`, and a Playwright route-mock factory would each
pay off within a release cycle. The polish-round-1 slice should batch
the first two; the Playwright factory is a strategy call (cross-ref
slice 333).

---

## Strengths

Counterbalance — five things the project does well that an audit must
name to stay honest:

1. **Stdlib `testing` everywhere.** No testify, no go-cmp dependency
   in the test-time module graph. Assertions use plain `if got != want { t.Fatalf(...) }`.
   This is the Go community canonical choice; it avoids vendor lock-in
   and keeps test failures parseable without a third-party DSL.
2. **The integration tier exists and is real.** It binds against a real
   Postgres + NATS + MinIO, runs migrations, applies role bootstraps,
   and audits RLS coverage as a workflow step. This is rare; most
   shops at this size mock the database in unit tests and have no
   integration tier at all. Article IX is honored in practice.
3. **The Go coverage ratchet is monotonic.** Slice 069's
   `cmd/scripts/coverage-gate` design enforces "raise the floor in the
   same PR as the tests" — the floor cannot lower. This is the right
   shape for a project that wants to grow coverage without
   regressing it.
4. **Playwright trace + screenshot + video on failure.** The config
   (`web/playwright.config.ts` lines 39-44) captures the full debugging
   context on every failure. Combined with the README's "How to debug a
   failure via the trace viewer" section, the time-to-diagnose for a
   flaky spec is short.
5. **Documented flake diagnosis discipline.** The chromedp flake
   investigation (slice 340) wrote a real diagnostic record at
   `docs/audit-log/340-chromedp-flake-decisions.md` instead of marking
   a test flaky and moving on. The slice doc + decisions log + the
   in-test comment chain on `render_integration_test.go` is the model
   for how flake debt should be retired.

---

## Pattern-density summary

| Rank | Surface             | High | Medium | Low | Disposition                                                              |
| ---- | ------------------- | ---- | ------ | --- | ------------------------------------------------------------------------ |
| 1    | Go integration      | 2    | 2      | 1   | Both high-severity items file as individual slices                       |
| 2    | Frontend vitest     | 1    | 2      | 2   | V-1 (ratchet) files as individual slice; rest bundle into polish round 1 |
| 3    | Go unit             | 0    | 2      | 3   | Bundle into polish round 1                                               |
| 4    | Frontend Playwright | 0    | 4      | 1   | Bundle into polish round 1; P-1 cross-refs slice 333                     |

**Total:** 3 High · 10 Medium · 7 Low = 20 findings across 4 surfaces.

**Spillover slice fan-out:** 4 (3 individual high + 1 polish-round).
Cap is 5; under cap. Per AC-3 each high-severity item is a tracer-bullet
slice; per AC-4 medium / low bundle into the polish-round slice.

---

## Cross-references

- **Slice 333 (`docs/issues/333-qa-strategy-gap-analysis.md`)** — the
  qa-expert strategy audit. Strategy-level overlaps with this
  framework-level audit on:
  - **Hybrid-mocked Playwright as the merged-gate "e2e" tier** (P-1
    here). Strategy view: is the `/e2e/` suite genuinely e2e, or is it
    a high-fidelity component-integration tier? The framework audit
    flags the mock density (57 `route.fulfill` calls); the strategy
    audit owns the "should we add a real-services e2e tier?"
    decision.
  - **Page Object Model adoption** (P-2 here). Strategy view: does the
    project want a structured POM layer, or does it accept testid-direct
    selectors as the convention? Framework audit observes the
    convention; strategy audit owns the call.
- **Slice 069 (`docs/issues/069-testing-discipline-ratchet.md`)** — the
  coverage ratchet contract. Strengths and gaps explicitly:
  - **Strength:** the Go-side ratchet (`cmd/scripts/coverage-gate` +
    `coverage-thresholds.json`) is the load-bearing primitive that
    keeps coverage from regressing.
  - **Gap:** the TS side ships measurement but no ratchet (V-1). Slice
    069's "informational, follow-up will raise the bar" promise has not
    been retired ~250 slices later.
  - **Gap:** the integration job's package list is hand-maintained
    (I-1). Slice 069 did not provide a discovery primitive; the
    enrolment retroactives (slices 279, 283, …, 320) are the visible
    cost.

---

## Spillover slices filed

Per Amendment 2 continuous-batch policy and slice doc AC-3 / AC-4:

| Slot | File                                                        | Type     | Severity bundle                                                 |
| ---- | ----------------------------------------------------------- | -------- | --------------------------------------------------------------- |
| 345  | `docs/issues/345-ci-integration-job-enrolment-discovery.md` | AFK      | I-1 (high) — discovery primitive for integration-test enrolment |
| 346  | `docs/issues/346-ci-yaml-history-extraction.md`             | JUDGMENT | I-2 (high) — move slice-by-slice commentary out of `ci.yml`     |
| 347  | `docs/issues/347-vitest-coverage-ratchet.md`                | AFK      | V-1 (high) — TS-side coverage ratchet                           |
| 348  | `docs/issues/348-test-framework-polish-round-1.md`          | AFK      | All medium + low findings across the four surfaces              |

Cap is 5; 4 filed. The slice doc allows individual or bundled
medium-severity slices; the polish-round bundle is the call here
because medium findings are small individually and cluster into
sub-themes the polish round can address coherently.

---

## Decisions log reference

Per slice 334 AC-2, a companion decisions log lives at
`docs/audit-log/334-test-framework-review-decisions.md` and captures
the per-framework current-state vs target-state vs gap vs remediation
table the slice doc's AC-2 schema requires. The report above is the
audit narrative; the decisions log is the structured artifact.
