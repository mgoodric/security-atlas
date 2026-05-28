# 333 — QA strategy gap analysis report

**Slice:** 333
**Date:** 2026-05-28
**Auditor:** `voltagent-qa-sec:qa-expert` persona (instance run)
**Scope:** read-only strategic QA audit; no test code, CI config, or product code modified

---

## Methodology

This audit applies the `voltagent-qa-sec:qa-expert` persona at
`~/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/qa-expert.md`
to the eight strategic-level QA dimensions named in the slice doc
(`docs/issues/333-qa-strategy-gap-analysis.md`). The persona's
"build a comprehensive QA program" lens is adapted to the project's
"strategy-level re-audit at ~250-slice scale" reality: the persona's
program-design sections (test strategy, defect management, quality
metrics) become rubrics for the audit, not implementation targets.

This slice is the **strategy view**. The companion slice 334 audit
(`docs/audits/334-test-framework-review.md`, persona
`test-automator`) covered the **framework view** — "are the four
frameworks well-configured?". This slice covers — "is the OVERALL
QA approach sound for a security-critical SaaS platform?" Findings
that overlap with slice 334 are noted in the cross-reference section
rather than duplicated.

### Severity mapping

- **High** = the strategy has a load-bearing gap that compounds across releases or hides a class of bug.
- **Medium** = the strategy is workable but drifting; remediation pays off within 2-3 release cycles.
- **Low** = the strategy is sound; a sharpening edit improves clarity.

### Audit surface (bounded)

| Dimension               | Signal sources surveyed                                                                                                                                                                         |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1. Test pyramid health  | `find . -name "*_test.go" \| wc -l` (334 Go test files); `find web -name "*.test.ts" \| wc -l` (115); `find web/e2e -name "*.spec.ts" \| wc -l` (47); LOC per tier                              |
| 2. Coverage strategy    | `cmd/scripts/coverage-thresholds.json` (197 floors, 61 excludes); slice 069 ratchet contract; slice 347 vitest ratchet (`web/vitest.config.ts`)                                                 |
| 3. Integration coverage | 116 `//go:build integration` files; `CLAUDE.md` "Testing discipline" four-surface gate; `internal/db/integration_test.go` real Postgres + NATS + MinIO bring-up                                 |
| 4. e2e coverage         | 47 Playwright specs in `web/e2e/`; `web/e2e-audit/` ui-honesty harness (informational only); 8 `test.skip` quarantines; critical-flow grep (tenant-switch, super-admin, board-export)           |
| 5. Mutation testing     | `go.mod` and `web/package.json` greps for `gremlins`, `stryker`, `mutmut` — all zero hits. Tier-recommendations block in `coverage-thresholds.json` is the project's stated test-quality model. |
| 6. Defect flow          | `docs/issues/*.md` recent slice naming pattern (e.g. 340 chromedp flake → 341 wsurlreadtimeout fanout); `docs/audit-log/` JUDGMENT-slice decisions logs                                         |
| 7. Flake economy        | `web/playwright.config.ts` `retries: isCI ? 1 : 0`; slice 340 + 341 (5 consecutive chromedp failures); `.github/workflows/ci.yml` retry policy grep                                             |
| 8. Test-data discipline | 15 `TestMain` blocks across `internal/`; `internal/demoseed/` (slice 205); `web/e2e/fixtures.ts` (post-slice-201); `oscal-bridge/` (Python, separate)                                           |

The bounded sample produced 2-3 strategic findings per dimension — the
slice doc's stated cap — so the audit was not expanded.

### Out of scope (deferred)

- **Mutation testing as in-slice work.** The slice doc explicitly defers this as a capability question, not a build-out. Findings below name the gap; remediation is a separate slice (or a deliberate "we don't need this yet" decision).
- **Per-package coverage drill-down.** That is slice 334's framework view. This slice asks whether the coverage MODEL is right, not whether package X's floor is right.
- **Manual / exploratory testing programs.** The project has none today (zero-FTE QA team is the operational reality). The audit notes this as a strategic implication but does not propose building a manual program — the project's risk profile prefers automation density over manual coverage.
- **Performance / load testing strategy.** Slice 332 (performance audit) owns that; out of scope here.
- **Security testing strategy.** Slices 327 / 329 / 338 own that; out of scope here.

---

## Findings — per dimension

### Dimension 1 — Test pyramid health

**Current state.** The project ships four enforced test surfaces. Counted by file:

| Surface         | Files | LOC (rough) | Tier in classic pyramid                       |
| --------------- | ----- | ----------- | --------------------------------------------- |
| Go unit         | 218   | ~49k        | Base                                          |
| Go integration  | 116   | ~52k        | Middle (real Postgres + NATS + MinIO)         |
| Frontend vitest | 115   | ~17k        | Base (module-level; `node` env, no DOM)       |
| Playwright e2e  | 47    | ~9k         | Top — but mocked upstream (see Theme 1 below) |

The shape is **integration-heavy, not unit-heavy**. The Go integration tier
has roughly the same LOC as the unit tier; this is the inverse of the
"large unit base, thin integration middle, tiny e2e tip" classical
pyramid. This is a **deliberate choice** justified by Article IX
(integration-first testing) and the "never mock the database" doctrine.

**Strategic gaps.**

- **Q-1 (High) — No contract-test tier between Go integration and Playwright e2e.** The integration tier exercises real services; the e2e tier mocks the BFF→atlas wire surface in 57 `route.fulfill` calls across 46 specs (slice 334 P-1). The **wire shape between web BFF and atlas API** is not tested anywhere as a contract — a BFF that drifts from atlas's actual response shape only fails when a Playwright spec happens to assert on that shape with a mock that matches reality. The `proto/` directory has gRPC schemas (`evidence.proto`, `connectors.proto`, `oscal.proto`, `admin/credentials.proto`) but no consumer-side contract tests against the HTTP shapes the frontend consumes. **Recommendation:** add a fifth surface — a thin contract-test tier — that pins the atlas HTTP API's response shapes (golden-file or schemathesis-style) so the e2e suite can keep its mocks but a real wire-drift gets caught. This is the missing tier the slice doc anticipated.

- **Q-2 (Medium) — The "unit tier" is mostly handler-level, not pure-function.** Spot-check: `internal/api/oauth/token_test.go`, `internal/api/controls/*_test.go`, `internal/eval/state_test.go` all exercise HTTP handlers or stateful constructors. The pure-function base of the pyramid (validators, normalizers, parsers, formatters) does exist but is buried — `internal/eval/helpers_test.go`, `internal/board/pdf_html_test.go`, the `helpers_test.go` pattern from slices 290/297/310/315/320. There is no convention that says "if you have a pure function, write a fast `t.Parallel()` unit test first." The result is acceptable today (integration coverage catches most regressions) but the base of the pyramid is **soft**, which means the test suite's wall-clock will scale linearly with feature surface forever — there is no fast feedback loop separate from the integration tier. **Recommendation:** formalize a "pure-Go pre-DB branches" convention (already practiced by slice 290 pattern) as the canonical way to lift a package's floor. Document it in `CLAUDE.md` "Testing discipline" or `Plans/canvas/09-tech-stack.md`.

- **Q-3 (Medium) — Vitest is structurally a node-only logic tier, not a true unit tier for the React app.** `web/vitest.config.ts:43` pins `environment: "node"` per slice 069 P0-A3 (no JSX, no DOM). The 115 vitest files exercise BFF route handlers, `lib/api.ts`, `lib/api/bff.ts` — Node-side logic. **Zero component-level tests exist** for the React surface. Component-level regressions (a misnamed testid, a missing accessibility attribute, a regression in a `<Button>` variant) are only caught by Playwright e2e. The e2e tier is slow (each spec is ~3-5s); a regression that should cost 50ms to detect costs 5s. **Recommendation:** decide explicitly whether the project wants a component-test surface (React Testing Library + happy-dom under vitest, separate `environment: "happy-dom"` project) or whether the e2e tier is the de facto component-test tier. The status quo (neither, by drift) is the gap.

**Density:** 1 high · 2 medium · 0 low = 3 findings. Q-1 is the load-bearing strategic call.

### Dimension 2 — Coverage strategy

**Current state.** Go side: `cmd/scripts/coverage-thresholds.json` enforces **197 per-package floors** + **61 excludes** via `cmd/scripts/coverage-gate`. Monotonic ratchet (slice 069 P0-A4: floors never lower). Floor formula: `floor(measured - 2pp)`. Merge gate.

TS side: slice 347 just landed (`web/vitest.config.ts` ratchet). Wires per-file thresholds via vitest's `coverage.thresholds` map. Monotonic but newer — the asymmetry called out as finding V-1 in slice 334 has just been closed.

**Strategic gaps.**

- **Q-4 (High) — Package-floor coverage is the wrong granularity for security-critical paths.** The Go ratchet enforces `internal/api/oauth: 75%` (or wherever it sits) as a single number, but the OAuth handler has dozens of error branches (invalid grant, expired code, revoked token, tenant-switch denied, super-admin escalation refused). A 75% line-floor is satisfied by happy-path coverage; the **dangerous error branches** can sit untested and the gate stays green. Article IX (integration-first) helps because integration tests tend to hit error paths in a way unit tests don't — but there is no explicit "branch-level floor for `internal/auth/*`, `internal/api/oauth/*`, `internal/api/authzmw`, `internal/api/oauth/revocation`, `internal/api/oauth/oauthclient`" discipline. **Recommendation:** introduce a "security-critical path tier" in `$tier_recommendations`: a small named list of packages where the floor is BRANCH coverage, not line coverage, and the floor is 90%+. The list is short (~10 packages); the discipline is targeted. Do NOT lift line-floors across the board — that's the wrong primitive.

- **Q-5 (Medium) — 61 excludes is structurally fine but politically dangerous.** Each exclude is individually justified (slice 069 review, slice 312 round-3 refresh, slice 320 demoseed enrolment), but **excludes are the path of least resistance for landing a slice without writing tests.** Already noted as finding U-2 of slice 334. Strategic view: an exclude-list-as-pressure-valve trends monotonically up over a project's lifetime; the right discipline is a quarterly "excludes retirement" pass where each exclude is either justified-in-writing or re-floored. **Recommendation:** add a quarterly maintenance task (not a slice — a maintainer review) that examines each exclude and either justifies it or files a slice to retire it. Track the count over time; if it grows >5% per quarter, file a remediation slice.

- **Q-6 (Medium) — Coverage measures lines, not assertions per assertion-eligible LOC.** This is the mutation-testing question framed as a coverage question (see Dimension 5). 80% line coverage with 1 assertion per 10 lines is not the same as 80% line coverage with 1 assertion per 2 lines. The project has no measurement of **assertion density** — a test that imports a package and runs `pkg.DoThing()` without asserting on the result lifts the line-coverage number while testing nothing. **Recommendation:** as a low-cost proxy for mutation testing, add a CI step that counts `t.Errorf`/`t.Fatalf`/`require.*`/`assert.*` calls per test file and emits a warning if the assertion-density is below a threshold (e.g. <1 per 20 LOC of test code). Cheap; would have caught the assertion-light tests that exist in the codebase (uncatalogued; spot-check showed several `_test.go` files where the test invokes a function and inspects a side effect with one fragile `strings.Contains` call — the U-3 pattern from slice 334 generalized).

**Density:** 1 high · 2 medium · 0 low = 3 findings. Q-4 is the load-bearing strategic call.

### Dimension 3 — Integration coverage

**Current state.** 116 `//go:build integration` files. The integration tier is **the strongest part of the QA program** — real Postgres + NATS + MinIO, role bootstraps, RLS coverage audit as a CI step, append-only invariant enforced via admin role. Article IX honored in practice.

**Strategic gaps.**

- **Q-7 (High) — Integration coverage is opt-in via build tag, with no audit that every integration-test-eligible package opts in.** The hand-maintained CI package list at `.github/workflows/ci.yml:515-568` is finding I-1 of slice 334. Strategic frame: the integration tier could be a comprehensive audit surface for the codebase, but it is enforced only on the packages a contributor remembered to enroll. The 17 retroactive enrolment slices (279, 283, 284, 287, 288, 290, 293, 294, 295, 297, 310, 313, 315, 317, 318, 319, 320) are the visible cost. Even after slice 345 closes the enrolment-discovery primitive (cross-ref), the strategic question remains: should new packages be **required** to ship an `integration_test.go` if they touch the database, OR is the policy "integration tests are best-effort, gated by the discovery primitive"? **Recommendation:** make the policy explicit. Two options: (a) "every package that imports `internal/db/dbx` or sets `app.current_tenant` ships an integration test, enforced by a CI lint" — strict; (b) "the discovery primitive surfaces gaps, the maintainer triages" — pragmatic. Pick one and document in `CLAUDE.md` "Testing discipline".

- **Q-8 (Medium) — `-p 1` is correctly load-bearing but it is also a wall-clock ceiling.** Slice 334 closes the `-p 1` rationale review. The integration job runs serially across packages — current wall-clock is ~10-15 min and growing as the package list grows. There is no plan for what happens when wall-clock crosses 30 min (developer patience ceiling for "did my PR pass?"). Slice 334's "future relaxation path (v3): split into Phase A serial + Phase B parallel" is the right shape but it is not on any backlog. **Recommendation:** add an explicit trigger: "when integration job wall-clock exceeds 20 min on a clean main run, file the Phase A/B split slice." This converts an undefined future problem into a tracked watermark. Today's wall-clock should be measured and recorded as the baseline.

**Density:** 1 high · 1 medium · 0 low = 2 findings. Q-7 is the load-bearing strategic call.

### Dimension 4 — e2e coverage

**Current state.** 47 Playwright specs in `web/e2e/`. Coverage includes admin-bootstrap, audit-workspace, control-detail, controls-list, dashboard, evidence-list, risk-hierarchy, audits-list, board-pack-detail, settings, super-admins, version-public, security-headers, mobile-baseline, more. The `web/e2e-audit/` ui-honesty harness exercises the production stack with real services but is **informational only** — not on the merged gate.

**Strategic gaps.**

- **Q-9 (High) — Critical security flows are unrepresented or skipped in the merged e2e gate.** Spot-check of critical multi-tenant flows:

  | Flow                                     | e2e spec status                                          |
  | ---------------------------------------- | -------------------------------------------------------- |
  | tenant-switch (multi-tenant user)        | **no spec found** — `grep tenant-switch web/e2e/*` empty |
  | super-admin operations                   | `super-admins.spec.ts` exists; status TBD                |
  | first-time-login + admin-bootstrap       | `first-time-login.spec.ts` + `admin-bootstrap.spec.ts`   |
  | auth-open-redirect (security regression) | `test.skip` — quarantined                                |
  | evidence push from CLI end-to-end        | `evidence-list.spec.ts` exists (UI side only)            |
  | board-pack export end-to-end             | `board-pack-detail.spec.ts` exists (UI side only)        |
  | bff-cookie-production-build              | `test.skip` — quarantined                                |
  | logo-render-production-build             | `test.skip` — quarantined                                |
  | questionnaires                           | `test.skip` — quarantined                                |
  | risks-create                             | `test.skip` — quarantined                                |

  8 specs carry `test.skip` (slice 334 P-4). Tenant-switch is the
  most important multi-tenant security flow and there is no spec at
  all. evidence-push-end-to-end (CLI through to UI display) is a
  v1 success-test flow and is only half-covered. **Recommendation:**
  audit critical flows against the v1 success test ("does the user
  run their next SOC 2 audit out of security-atlas?") and file
  individual slices for the missing ones. Tenant-switch is P0;
  evidence-push-end-to-end is P1; the rest is judgment.

- **Q-10 (Medium) — `e2e-audit/` (ui-honesty) is the strongest e2e surface in the codebase and it is not on the merged gate.** This is the strategic finding. The `web/e2e-audit/` harness runs Playwright against a production-build stack with real services (no `route.fulfill` mocks). It exists. It is documented. It is wired into CI as `frontend-ui-honesty`. It is **informational only** — failures do not block merge. The merged gate is `frontend-playwright`, which runs the mocked-upstream suite. The asymmetry is: the project has a true e2e tier and a hybrid-mocked e2e tier; the hybrid-mocked one is the gate. **Recommendation:** stage promotion of the ui-honesty harness from informational to merge-blocking. The path: (a) audit which `ui-honesty.spec.ts` assertions are stable across CI runs over 30 days; (b) move stable assertions to a merged sub-gate; (c) keep the volatile ones informational; (d) over time, migrate the assertion logic from `e2e/` mocked specs to `e2e-audit/` real specs. This is a multi-slice path; the strategic question is whether it is the right direction (yes) or whether the project accepts hybrid-mocked-e2e as the gate forever.

**Density:** 1 high · 1 medium · 0 low = 2 findings. Q-9 is the load-bearing strategic call.

### Dimension 5 — Mutation testing readiness

**Current state.** Zero adoption. `gremlins` not in `go.mod`; `stryker` not in `web/package.json`. No mutation-score measurement anywhere. The `$tier_recommendations` block in `coverage-thresholds.json` (slice 279) is the project's stated test-quality model and it is line-coverage-based.

**Strategic gaps.**

- **Q-11 (Medium) — Mutation testing readiness is high; adoption is zero.** Readiness means: the test suite is fast enough per-package that a mutation tool could realistically run it (Go unit tier averages <30s per package; the integration tier is too slow for mutation testing today). The infrastructure is also there — `coverage-gate` already builds per-package profiles; a mutation gate could ride on the same plumbing. The friction is **organizational**, not technical: nobody has been asked to interpret a mutation score or set a target. **Recommendation (NOT in-slice — slice doc anti-criteria forbid mutation-testing-as-in-slice-work):** file a discovery slice that runs `gremlins` on a single high-value package (e.g. `internal/auth/jwt` or `internal/api/oauth/oauthcode`) for one week, reports the mutation score, and proposes a target. Do NOT roll out broadly; this is an "is the juice worth the squeeze?" pilot. The slice doc explicitly defers this to a follow-on.

- **Q-12 (Low) — Mutation testing would surface the assertion-density gap (Q-6 above) as a measurable number.** This is the meta-observation: the two gaps are the same gap viewed at different scales. A test suite with high line coverage but low assertion density has a low mutation score; a test suite with low line coverage but high assertion density has a higher-than-expected mutation score. **Recommendation:** the cheap proxy (Q-6 assertion-density CI step) is a v1 substitute; the real mutation-testing answer is a v3 path.

**Density:** 0 high · 1 medium · 1 low = 2 findings. Both bundle into a future-evaluation slice; neither is in-slice work.

### Dimension 6 — Defect flow

**Current state.** No formal bug-tracking taxonomy. Bug-discovery → fix path goes through GitHub issues (the issue list is largely empty per `gh issue list` snapshot), the slice backlog, and post-merge revert-or-fix-forward decisions. Recent flake examples (slice 340 + 341) show the project DOES record root-cause in decisions logs when investigation completes — that is the right shape.

**Strategic gaps.**

- **Q-13 (Medium) — There is no defect-detection-tier classification.** When a bug is found, the project does not record where it WAS caught — production? integration? Playwright? code review? — nor where it SHOULD have been caught. The slice 340 + 341 chromedp investigation is the exception, not the rule; it has a decisions log with diagnostic procedure + reproducer. The 70% of post-merge fix-forward commits in the `feedback_*` MEMORY entries (postgres constraints, RLS, sqlc regen, GHA service containers) do not record this classification. **Recommendation:** add a one-line field to JUDGMENT-slice decisions logs: `detection_tier_actual` (where the bug was caught) and `detection_tier_target` (where the bug SHOULD have been caught — `unit`, `integration`, `playwright`, `contract`, `manual_review`, `production`). Aggregate quarterly. A pattern of "should-have-been-unit-but-was-production" 4× per quarter is a coverage-tier gap; a pattern of "should-have-been-integration-but-was-fix-forward" 6× per quarter is an integration-enrollment gap (Q-7 above).

- **Q-14 (Low) — Defect leakage to production is unmeasurable today.** Related to Q-13 but framed differently: the project has no measurement of "how many bugs reached production despite the four-surface gate?" because there is no production-vs-pre-prod distinction in the issue stream (the project is OSS + self-hostable; there is no SRE-owned production environment). **Recommendation:** for self-hosters who have opted into telemetry, count bug-reports-from-the-wild per release; for the OSS project itself, the canonical proxy is "fix-forward commits per merged slice" — already informally tracked in `_STATUS.md`'s `UNSTABLE` annotations. Formalize the metric as `slices_merged_with_fix_forward / total_slices_merged` per quarter.

**Density:** 0 high · 1 medium · 1 low = 2 findings. Q-13 is the load-bearing strategic call but its cost is low (one line per decisions log).

### Dimension 7 — Flake economy

**Current state.** No documented flake budget. Playwright config sets `retries: isCI ? 1 : 0` — one retry policy. Slice 340 + 341 chromedp investigation is the canonical recent example (5 consecutive failures, then `t.Skip` quarantine, then root-cause diagnosis via `wsURLReadTimeout`). The chromedp test was eventually re-enabled per slice 340's decisions log.

**Strategic gaps.**

- **Q-15 (High) — There is no flake budget. The default is "any flake blocks merge; investigate to root cause."** This is a strong default — it caught the chromedp regression. It is also a strong default — it means the project trades wall-clock for confidence on every flake, and during high-velocity batches (the 50+-slice v2 drain session, the auth-substrate-v2 spine) the flake-investigation overhead is non-trivial. **Recommendation:** propose an explicit flake budget. Suggested shape:

  - **Target flake rate per surface:** Go integration <0.5%, Playwright <1%, vitest ~0%
  - **Retry policy:** 1 retry on flaky failure (already in `playwright.config.ts`); on second failure, treat as hard fail
  - **Flake-debt cap:** when total observed flake rate exceeds target for 2 consecutive weeks, **no new specs land** in the affected surface until the debt is paid (i.e. flake rate returns to target)
  - **Investigation budget:** if a flake is observed 3× in a week, file an investigation slice (slice 340 pattern); if observed once, retry-and-watch is acceptable
  - **Visibility:** publish flake rate per surface per week in a `docs/flake-budget.md` dashboard updated by a CI cron

  Without these numbers, "flaky vs hard fail" is a per-incident judgment call; with them, the policy is mechanical. The slice doc explicitly anticipates this finding ("AC-5: decisions log includes a **flake budget proposal**").

- **Q-16 (Medium) — The integration tier's wall-clock and the merged-gate's strict-mode discipline are in tension.** Slice 322 (strict-mode discipline) raised the bar on what "passing CI" means; slice 069's ratchet contract enforces coverage floors. Both add to the merge-friction budget. The Playwright tier has `retries: 1` (CI), which softens strict-mode for that surface; the Go integration tier has no retry-on-flake. A flake there is a hard fail, which is correct for catching real bugs but unforgiving for genuinely transient issues (NATS startup race, Postgres `pg_isready` race, MinIO bucket-create race). **Recommendation:** decide explicitly whether the Go integration tier should have a `-retry 1` policy or whether the existing "investigate every flake" discipline is the contract. Today the policy is implicit; either choice is defensible, but the implicit version means each flake forces a re-litigation.

**Density:** 1 high · 1 medium · 0 low = 2 findings. Q-15 is the load-bearing strategic call.

### Dimension 8 — Test-data discipline

**Current state.** Three test-data realities coexist:

- **Go integration fixtures.** 15 `TestMain` blocks across `internal/` packages (slice 334 finding I-4). Each creates its own `appPool` + `adminPool` and seeds package-local rows.
- **`internal/demoseed/`** (slice 205). Canonical demo dataset for the platform. Idempotent Apply / Teardown. Used by the docker-compose self-host bring-up and the `e2e-audit/` ui-honesty harness.
- **Playwright fixtures.** `web/e2e/fixtures.ts` post-slice-201 — JWT-based per-worker auth (slice 197 → 201 migration). Mocks the upstream atlas API in 57 places.

**Strategic gaps.**

- **Q-17 (Medium) — Three test-data realities is two too many.** The asymmetry is: the integration tier's seeds are PER-PACKAGE; demoseed is GLOBAL; Playwright fixtures are HYBRID (a real per-worker JWT but mocked upstream responses). A test that asserts "the dashboard shows N controls" reads from a different N in each surface. **Recommendation:** designate `internal/demoseed` as the canonical reality and migrate per-package integration-test seeds to compose on top of it (or explicitly Teardown-then-seed-fresh for tests that need a deterministic minimal state). Two-cycle migration: (1) Add a `demoseed.ApplyMinimal()` helper that seeds only the rows shared across multiple packages (SCF anchors, framework versions, default tenant); (2) Move per-package seeds to compose on `ApplyMinimal()` for tests that need shared rows. The `-p 1` collision surface (slice 334's analysis of `evidence_kind_schemas`, `scf_anchors`, `evidence_records`) is exactly the surface where this discipline pays off.

- **Q-18 (Low) — Playwright fixtures and `e2e-audit` fixtures are independent.** `web/e2e/fixtures.ts` and `web/e2e-audit/fixtures.ts` are separate files with separate JWT-acquisition flows. Strategy frame: when Q-10 (promote ui-honesty to merge-blocking) lands, these two fixtures should converge. Today both exist and work; tomorrow's merge raises this. **Recommendation:** track as a follow-on under the Q-10 promotion path; not a separate slice.

**Density:** 0 high · 1 medium · 1 low = 2 findings. Q-17 is the load-bearing strategic call.

---

## Cross-framework themes

Three themes span 3+ dimensions:

### Theme 1 — The hybrid-mocked e2e tier is the project's biggest QA-strategy bet

Findings Q-1 (missing contract tier), Q-9 (critical flow gaps), Q-10 (ui-honesty not merge-blocking), Q-17 (three test-data realities) all orbit the same load-bearing decision: **is the merged `web/e2e/` suite genuinely end-to-end or is it a high-fidelity component-integration tier with a "Playwright" label?** Slice 334 P-1 framed this at the framework level (mock density). The strategic frame is: the project ships an OSS GRC platform whose customers will diligence the diligence tool — the merged-gate test suite needs to catch wire-shape drift between the BFF and atlas, multi-tenant security flow regressions, and production-build-only failures. The current suite cannot, by design. The `e2e-audit/` harness can, by design, but is informational. The strategic call (Q-10) is whether to invest in promoting ui-honesty or whether to add a contract-test tier (Q-1) and keep the e2e mocks. **Both are defensible**; doing neither is the failure mode.

### Theme 2 — Excludes / opt-in / hand-maintained lists are the silent enrolment risk

Findings Q-5 (coverage excludes growth), Q-7 (integration enrolment), Q-15 (flake budget absence) all share a structural pattern: the project relies on **opt-in registration** with a hand-maintained list, and the list silently bit-rots. The 17-slice retroactive enrolment trail for integration (Q-7) is the visible cost; the 61-entry coverage excludes is the latent cost; the absence of a flake budget is the cost of having no list at all. The strategic frame is: **lists should be derived from code, not hand-maintained.** Each retroactive-enrolment slice was a paper cut at the time; in aggregate they are a real signal that the QA enrolment model is wrong-shaped. Slice 345 (CI integration job enrolment discovery) is the right fix for Q-7; the analog for Q-5 (coverage excludes) is a CI lint that asserts every exclude has a `$justification` field; the analog for Q-15 is the flake-budget dashboard.

### Theme 3 — The project has a strong root-cause discipline and a weak detection-tier classification

Findings Q-13 (no detection-tier classification), Q-14 (defect leakage unmeasurable), Q-15 (no flake budget) all reflect the same gap: the project records **what was wrong** (excellent decisions logs, slice 340 / 341 chromedp investigation) but not **where it should have been caught**. The 70% post-merge fix-forward pattern visible in MEMORY.md (`feedback_postgres_constraints`, `feedback_ci_secret_scanning`, `feedback_ci_service_containers`, `feedback_local_vs_ci_delta`) shows the project DOES learn from each incident, but the learning is anecdotal — there is no aggregate signal "we are catching 80% of pgx bugs at unit, 15% at integration, 5% in production." The discipline exists at the incident level; the strategic gap is the aggregate.

---

## Flake budget proposal (AC-5 of the slice doc)

Per slice doc AC-5: the audit must propose a flake budget even if the answer is "we don't have one and should formalize one." The proposal:

| Surface         | Target flake rate | Retry policy     | Investigation trigger         | Debt cap                                          |
| --------------- | ----------------- | ---------------- | ----------------------------- | ------------------------------------------------- |
| Go unit         | 0%                | None (hard fail) | 1 flake = investigation slice | Any flake blocks the surface                      |
| Go integration  | <0.5% per package | None (hard fail) | 2 flakes in 7d = slice        | 3+ flakes in 14d = surface freeze on new packages |
| Frontend vitest | ~0%               | None (hard fail) | 1 flake = investigation slice | Any flake blocks the surface                      |
| Playwright e2e  | <1% per spec      | 1 retry in CI    | 2 flakes in 7d = slice        | 5% surface-wide flake rate = no new specs land    |

**Visibility shape.** A `docs/flake-budget.md` dashboard updated weekly
by a CI cron (or manually by the maintainer if cron is overkill).
Columns: surface, week, flake rate, target, status (green / yellow / red),
top-3 flaking tests.

**Observed baseline (slice 340 + 341 evidence).** chromedp PDF render was 5 consecutive failures across batches 125-129 — far above any reasonable budget. Slice 340's root-cause-and-fix discipline is the correct response. The budget exists to make the response **automatic**: 2 consecutive flakes = file the slice. Today that decision is judgment; with the budget it is mechanical.

**Why this matters.** The slice 340 investigation was 1-2d of focused work. Spread across the velocity of the v2 drain session, 4-5 such investigations per quarter is 1-2 weeks of maintainer time. A flake budget converts "investigate when it bothers me" into a tracked watermark, which converts maintainer time into a predictable cost.

---

## Strengths

Counterbalance — five things the project does well that an audit must
name to stay honest:

1. **Article IX (integration-first) is honored in practice, not just in canvas.** The 116 integration tests against real Postgres + NATS + MinIO are the strongest part of the QA program. Most shops at this size mock the database; this project does not. The wall-clock cost is real (Q-8) but the safety it buys is real and load-bearing.

2. **Slice 069's monotonic coverage ratchet is the right primitive.** Floors can only rise. Excludes are explicit. The 197-floor + 61-exclude inventory is large but tractable; the alternative ("we have 80% coverage on main, let's keep it above 75%") is what every other project does and what every other project regresses on.

3. **The JUDGMENT-slice decisions-log discipline is unusual and excellent.** Slice 340 (`docs/audit-log/340-chromedp-flake-decisions.md`) records diagnosis trail, root cause, reproducer, and confidence. Slice 341 (chromedp wsurlreadtimeout fanout) extends the pattern. Most shops do post-incident reviews; this project does post-incident **decisions logs** that future contributors can read. The discipline scales.

4. **Slice 322's strict-mode CI is the right shape.** Failed tests block merge. Slow tests block merge. Flaky tests block merge. The project pays a velocity cost for this and the cost is correctly priced — it is the cost of being shippable.

5. **`internal/demoseed/` exists and is idempotent.** Slice 205's demo dataset is the closest thing the project has to a canonical test-data reality. Most shops have ad-hoc fixtures per package and never invest in a shared seed; this project did. The Q-17 finding ("three realities, two too many") is a recommendation to extend the demoseed investment, not to start one.

---

## Pattern-density summary

| Dimension                     | High | Medium | Low | Disposition                                                                                        |
| ----------------------------- | ---- | ------ | --- | -------------------------------------------------------------------------------------------------- |
| 1. Test pyramid health        | 1    | 2      | 0   | Q-1 individual slice (contract tier); Q-2/Q-3 bundle into tactical round                           |
| 2. Coverage strategy          | 1    | 2      | 0   | Q-4 individual slice (security-critical branch floor); Q-5/Q-6 bundle into tactical round          |
| 3. Integration coverage       | 1    | 1      | 0   | Q-7 individual slice (integration enrolment policy); Q-8 bundle into tactical round                |
| 4. e2e coverage               | 1    | 1      | 0   | Q-9 individual slice (critical flow gaps audit); Q-10 individual slice (ui-honesty promotion path) |
| 5. Mutation testing readiness | 0    | 1      | 1   | Both bundle — deferred per slice doc anti-criteria; track only                                     |
| 6. Defect flow                | 0    | 1      | 1   | Q-13 / Q-14 bundle into tactical round (1-line addition to decisions log convention)               |
| 7. Flake economy              | 1    | 1      | 0   | Q-15 individual slice (flake budget proposal); Q-16 bundle into tactical round                     |
| 8. Test-data discipline       | 0    | 1      | 1   | Q-17 individual slice (demoseed convergence path); Q-18 cross-refs Q-10                            |

**Total:** 5 High · 10 Medium · 3 Low = 18 findings across 8 dimensions.

**Spillover slice fan-out (cap = 5):**

- Strategic (individual): Q-1 (contract tier), Q-4 (branch floor for security-critical), Q-9 (critical flow audit), Q-15 (flake budget) — **4 individual slices**
- Tactical (bundled): all medium/low findings — **1 bundled slice**

**Total: 5 spillover slices = cap.** Q-7 (integration enrolment policy) is folded into the tactical bundle because slice 345 already addresses the mechanical side; the strategic "policy decision" portion is a docs-and-CLAUDE.md edit best done alongside the broader tactical round.

---

## Cross-references

- **Slice 334 (`docs/audits/334-test-framework-review.md`)** — the
  test-automator framework-level audit. This slice is the strategy
  view; slice 334 is the framework view. Overlapping findings:

  - **Hybrid-mocked Playwright** (slice 334 P-1, this slice Q-1 + Q-10).
    Slice 334 catalogued the mock density; this slice owns the
    strategic call (add contract tier vs promote ui-honesty).
  - **Vitest coverage ratchet** (slice 334 V-1, this slice slice 347
    closes). Closed by slice 347's vitest ratchet.
  - **Integration enrolment** (slice 334 I-1, this slice Q-7).
    Slice 334 catalogued the hand-maintained list and the 17-slice
    retroactive enrolment trail; this slice owns the strategic
    "should new packages be **required** to ship an integration test?"
    policy question.

- **Slice 347 (`docs/issues/347-vitest-coverage-ratchet.md`)** —
  vitest coverage ratchet, **merged**. Resolves the TS-side asymmetry
  with the Go-side ratchet. With slice 347 landed, the measurement
  symmetry between Go and TS is restored, and the remaining coverage-
  strategy findings (Q-4, Q-5, Q-6) are about granularity and
  discipline, not about measurement.

- **Slice 069 (`docs/issues/069-verification-suite.md`)** — the
  testing-discipline contract. Strengths (monotonic ratchet, per-
  package floors, exclude discipline) are documented above; gaps
  (line-floor vs branch-floor for security-critical paths Q-4;
  exclude growth Q-5; assertion density Q-6) are the strategic
  follow-ons.

- **Slice 340 (`docs/audit-log/340-chromedp-flake-decisions.md`)** —
  the canonical recent flake-investigation log. Cited as the model
  for what the flake budget's "investigation trigger" output should
  look like (Q-15).

- **Slice 322 (strict-mode discipline)** — the merge-block contract.
  In tension with Q-16 (integration tier retry policy); both are
  defensible; the strategic call is "decide explicitly."

- **CLAUDE.md "Testing discipline"** — the four-surface gate
  documented in the project's top-level contract. The strategic
  findings (Q-1 missing contract tier, Q-10 ui-honesty promotion,
  Q-15 flake budget) all touch this contract; a future revision of
  CLAUDE.md should reflect resolved findings.

---

## Spillover slices filed

Per Amendment 2 continuous-batch policy and slice doc AC-3 / AC-4:

| Slot | File                                                         | Type     | Severity bundle                                                                                                                                                                                                                                                                                     |
| ---- | ------------------------------------------------------------ | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 349  | `docs/issues/349-contract-test-tier-evaluation.md`           | JUDGMENT | Q-1 (high) — evaluate adding a BFF↔atlas wire-shape contract-test tier                                                                                                                                                                                                                             |
| 350  | `docs/issues/350-branch-coverage-floor-security-critical.md` | AFK      | Q-4 (high) — branch-coverage floor for `internal/auth/*`, `internal/api/oauth/*`                                                                                                                                                                                                                    |
| 351  | `docs/issues/351-e2e-critical-flow-gap-audit.md`             | AFK      | Q-9 (high) — audit critical multi-tenant flows + file individual specs for the gaps                                                                                                                                                                                                                 |
| 352  | `docs/issues/352-flake-budget-dashboard.md`                  | AFK      | Q-15 (high) — formalize the flake budget proposed in this audit; ship dashboard + thresholds                                                                                                                                                                                                        |
| 353  | `docs/issues/353-qa-strategy-tactical-round-1.md`            | AFK      | All medium + low findings across 8 dimensions; demoseed convergence (Q-17), integration policy (Q-7), excludes maintenance (Q-5), assertion density (Q-6), defect taxonomy (Q-13), retry policy (Q-16), ui-honesty promotion path (Q-10), pure-Go base (Q-2), component-test surface decision (Q-3) |

Cap is 5; 5 filed. Strategic findings fan individually per AC-3;
tactical findings bundle into one round-1 slice per AC-4.

Mutation-testing (Q-11 / Q-12) is deliberately NOT filed as a
spillover per slice doc anti-criteria. A future slice (post the
Q-15 flake budget landing) can evaluate the `gremlins` pilot
described in Q-11.

---

## Decisions log reference

Per slice 333 AC-2, a companion decisions log lives at
`docs/audit-log/333-qa-strategy-gap-analysis-decisions.md` and
captures the per-dimension current-state · target-state · gap ·
remediation table the slice doc's AC-2 schema requires. The report
above is the audit narrative; the decisions log is the structured
artifact.
