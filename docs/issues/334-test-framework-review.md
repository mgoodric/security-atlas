# 334 — Test framework review via voltagent-qa-sec:test-automator

**Cluster:** Quality
**Estimate:** 1.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Runs `voltagent-qa-sec:test-automator` against the project's four
enforced test frameworks: Go unit, Go integration (`-tags=integration`),
frontend vitest, frontend Playwright e2e. This is the
framework-level audit — companion to slice 333's strategy-level one.

Where slice 333 asks "is the QA approach sound?", this slice asks
"are the four frameworks well-configured + free of anti-patterns +
sustainable as the project grows?"

**Audit surface.** Framework-by-framework pass:

- **Go unit (`go test ./...`).** Per-package test layout, fixture
  helpers, table-driven test patterns, golden-file usage, test
  parallelism (`t.Parallel()` discipline), assertion library choice
  (stdlib vs testify vs custom), coverage flag consistency
  (`-coverpkg=./...` per slice 069), `-race` flag usage.
- **Go integration (`-tags=integration -p 1`).** RLS test pattern
  (`internal/db/integration_test.go` is the canonical reference),
  test-fixture cleanup discipline, real-services binding (docker
  containers vs in-process), test-data isolation across parallel
  packages, `-p 1` serialization rationale + cost.
- **Frontend vitest.** Test file colocation, mocking discipline (BFF
  vs DOM), `lib/api.ts` vs `lib/api/bff.ts` test patterns, fixture
  organization, coverage upload (post slice 069), snapshot test
  policy.
- **Frontend Playwright e2e.** Spec-file organization (`web/e2e/` vs
  `web/e2e-audit/` — slice 178's separation), Page Object Model
  adoption, fixture preconditions, retry policy, screenshot/trace
  capture, JWT migration completeness (post slice 201), browser
  matrix (chromium only vs chromium + firefox + webkit).
- **CI integration.** Per-framework GitHub Actions job structure,
  artifact upload patterns (vitest `coverage-summary.json`,
  Playwright HTML report + traces), required-vs-informational gate
  classification.
- **Coverage ratchet (cross-framework).** Slice 069's
  `cmd/scripts/coverage-thresholds.json` model — does it cleanly
  scale to TS coverage too? Or is TS in a different model?
- **Test selection.** Can a developer run "just the tests that
  cover the changed code"? `go test -run` patterns, vitest
  `--related`, Playwright `--grep`.
- **Fixture portability.** Test fixtures used in CI must be
  reproducible locally (and vice versa). Are there CI-only or
  local-only fixtures that violate this?

**Why now:** four frameworks accumulating ~250 slices of patterns
will have drift. Better to catch it at strategy-time than during a
flaky-CI fire.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 8/12.

**Disposition:** read-only framework audit + follow-up-slice fan-out.

## Threat model

Framework-audit-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/334-test-framework-review-decisions.md`.
- **I (Information disclosure):** Test files are part of the OSS
  repo — no confidentiality concerns. CLEAN.
- **D (Denial of service):** CLEAN.
- **E (Elevation of privilege):** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:test-automator` agent runs
      against the four frameworks + CI integration + coverage
      ratchet + test selection + fixture portability.
- [ ] **AC-2.** Findings recorded in
      `docs/audit-log/334-test-framework-review-decisions.md` per
      framework: pattern · adoption rate · deviation count ·
      remediation path.
- [ ] **AC-3.** **Anti-pattern findings** (e.g. "package X mocks the
      DB in unit tests, violating CLAUDE.md doctrine") fan out as
      individual `/idea-to-slice` follow-up slices.
- [ ] **AC-4.** **Convention-drift findings** (e.g. "Page Object
      Model adoption is 30% across Playwright specs") bundle into a
      single "test framework polish round 1" slice OR per-framework
      individual slices.
- [ ] **AC-5.** Cross-references slice 333 (qa-expert strategy
      audit) — findings that overlap noted.
- [ ] **AC-6.** Cross-references slice 069 (coverage ratchet
      contract) — the ratchet's strengths + gaps explicitly named.
- [ ] **AC-7.** No code modified. Diff = doc files only.
- [ ] **AC-8.** The decisions log explicitly addresses the
      **integration-tier `-p 1` serialization** — is it still
      load-bearing (RLS test pattern requires it) or could it be
      relaxed?
- [ ] **AC-9.** `pre-commit run --files` passes.

## Constitutional invariants honored

- **Test-First Imperative (Article III).** Framework patterns must
  support test-first execution.
- **Integration-First Testing (Article IX).** The integration tier's
  real-services binding is verified.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — test stack choices
- `Plans/canvas/01-vision.md` §6 — survive third-party review

## Dependencies

- **#069** (testing discipline) — `merged`. Framework gate +
  ratchet contract.
- **#178** (UI honesty audit harness) — `merged`. Playwright-tier
  separation pattern.
- **#201** (Playwright JWT migration) — `merged`. e2e auth model.

## Anti-criteria (P0 — block merge)

- **P0-334-1.** Does NOT bundle anti-pattern findings into one
  slice. Tracer-bullet per anti-pattern.
- **P0-334-2.** Does NOT propose introducing mocks into Go
  integration tier (Article IX violation).
- **P0-334-3.** Does NOT propose removing the `-p 1` serialization
  flag without an explicit RLS-test-pattern preservation argument.
- **P0-334-4.** Does NOT auto-merge.
- **P0-334-5.** Does NOT modify code.
- **P0-334-6.** Does NOT cross into territory of slice 333 — when
  a strategy-level finding surfaces (rather than framework-level),
  cross-reference it instead of expanding scope.
- **P0-334-7.** Does NOT touch CLAUDE.md, canvas.

## Skill mix

- `voltagent-qa-sec:test-automator` — the named audit agent
- `/idea-to-slice` — for follow-ups
- Standard read/grep — test-file enumeration

## Notes for the implementing agent

**Reference points (canonical patterns to compare against):**

- Go unit: most-recent `internal/auth/*_test.go` (post-slice-187
  pattern with `t.Parallel`, table-driven, real types)
- Go integration: `internal/db/integration_test.go` (the canonical
  RLS test pattern)
- Vitest: `web/lib/api/bff.test.ts` (BFF test pattern post slice 211)
- Playwright e2e: `web/e2e/admin-demo.spec.ts` (post-slice-322
  pattern; AC-4 contract for visible-feedback)
- Playwright audit harness: `web/e2e-audit/lib/heuristics.ts`
  (slice 178's pattern)

Deviations from these references are findings. The audit isn't
asking "is the reference perfect?" but "is adoption consistent?"

**Cross-reference with slice 333.** This slice is framework-level;
slice 333 is strategy. A finding like "we don't have a contract
test tier" is strategy (slice 333's land); "Go unit tests in
package X mock the DB" is framework (this slice's land).

**Audit log filename:**
`docs/audit-log/334-test-framework-review-decisions.md`
