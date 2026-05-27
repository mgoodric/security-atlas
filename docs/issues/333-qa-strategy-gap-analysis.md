# 333 — QA strategy gap analysis via voltagent-qa-sec:qa-expert

**Cluster:** Quality
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Runs `voltagent-qa-sec:qa-expert` against the project's current QA
strategy and surfaces gaps in the test pyramid + coverage strategy +
defect-flow + mutation-testing readiness. This is the
strategic-level QA audit: not "is package X tested?" but "is the
overall approach sound for a security-critical SaaS platform?"

The project has four enforced test surfaces (per CLAUDE.md): Go unit,
Go integration, frontend vitest, frontend Playwright. Coverage
ratchet contract (slice 069) governs per-package floors. ~250 slices
have shipped. This audit asks: does the QA strategy match the
product's risk profile, and where is it brittle?

**Audit surface.** Strategic QA pass across:

- **Test pyramid health.** Are unit / integration / e2e ratios
  appropriate? Today the integration tier is the only one with
  RLS-enforcement coverage (real Postgres); unit tier mocks DB
  per the "never mock the DB" doctrine. Playwright e2e covers user
  flows but is not in the merged-coverage gate. Diagnose whether the
  shape is right for the risk profile.
- **Coverage strategy.** Slice 069's ratchet contract is monotonic
  but the floor is package-level. Are there packages where the
  package-level floor is satisfied but the BRANCH-level coverage on
  a load-bearing path is poor (e.g. error paths in `internal/auth/*`)?
- **Integration coverage.** The integration tier (`-tags=integration`)
  uses real Postgres + NATS + MinIO. Are there code paths that
  appear unit-tested but are not exercised against real services?
  (False sense of safety.)
- **e2e coverage.** Playwright tests cover specific flows. Are
  critical flows uncovered? (Tenant-switch, super-admin operations,
  evidence-push-from-CLI, board-pack export end-to-end.)
- **Mutation testing readiness.** Could the project run mutation
  testing (Go: e.g. `gremlins`, TS: e.g. `stryker`)? What would the
  realistic mutation score look like? This is the test-suite quality
  meta-question.
- **Defect flow.** From bug-discovery to fix: how many bugs in the
  current `gh issue list` are e2e-detectable vs unit-detectable vs
  manual-testing-only? Pattern-find the gaps.
- **Flake economy.** The integration tier has chromedp PDF render
  flakes documented (per recent batches). What's the total flake
  budget? Is the flake-budget compatible with the CI ratchet?
- **Test-data discipline.** Demo seed (slice 205), Playwright
  fixtures (post slice 201's JWT migration), Go integration fixtures
  — are these aligned, or are there three independent test-data
  realities the project maintains?

**Why now:** the four-surface gate (CLAUDE.md "Testing discipline")
was set during slice 069. The project has expanded substantially
since; a strategy-level re-audit identifies where the existing model
is now mismatched to the risk profile.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 7/12.

**Disposition:** read-only strategy audit + follow-up-slice fan-out.

## Threat model

QA-strategy-audit-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/333-qa-strategy-gap-analysis-decisions.md`.
- **I (Information disclosure):** Findings describe test gaps in
  specific packages — a roadmap to find an untested-path attack
  surface. **Mitigation:** describe gaps at strategy level
  ("error-path coverage in `internal/auth/*` is below target")
  rather than exploit level ("specifically the X line is unreachable
  by current tests, here's how to abuse it"). Same discipline as
  slice 327 / 329.
- **D (Denial of service):** CLEAN.
- **E (Elevation of privilege):** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:qa-expert` agent runs against
      the eight strategy dimensions in the narrative.
- [ ] **AC-2.** Findings recorded in
      `docs/audit-log/333-qa-strategy-gap-analysis-decisions.md`
      per dimension: current-state · target-state · gap ·
      remediation path.
- [ ] **AC-3.** **Strategic findings** (e.g. "the test pyramid has
      no contract-test tier; should it?") fan out as individual
      `/idea-to-slice` follow-up slices.
- [ ] **AC-4.** **Tactical findings** (e.g. "package X is missing
      integration coverage for endpoint Y") bundle into a "QA
      tactical gaps round 1" tracking slice OR per-area individual
      slices.
- [ ] **AC-5.** The decisions log includes a **flake budget
      proposal** based on observed flake rates (chromedp PDF render
      is the canonical example). Even if "we don't have one and
      should formalize one" is the answer.
- [ ] **AC-6.** Cross-references slice 334 (test-automator agent) —
      this slice is the strategy view; slice 334 is the framework
      view. Same finding can show up in both — dedupe.
- [ ] **AC-7.** No code modified. Diff = doc files only.
- [ ] **AC-8.** Cross-references slice 069 (testing discipline) — the
      ratchet contract's strengths and gaps explicitly.
- [ ] **AC-9.** `pre-commit run --files` passes.

## Constitutional invariants honored

- **Test-First Imperative (Article III).** The strategy-level audit
  asks whether TDD discipline holds across the codebase at scale.
- **Integration-First Testing (Article IX).** The audit specifically
  checks whether the integration tier exercises real environments
  or whether mocks have crept in.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — test stack
- `Plans/canvas/01-vision.md` §6 — survive third-party review

## Dependencies

- **#069** (testing discipline) — `merged`. The ratchet contract is
  the primary surface.
- **#201** (Playwright JWT migration) — `merged`. e2e-tier auth
  reality.
- **#205** (demo seed) — `merged`. Test-data discipline anchor.

## Anti-criteria (P0 — block merge)

- **P0-333-1.** Does NOT bundle Strategic findings into one slice.
  Each strategic question = one tracer-bullet slice.
- **P0-333-2.** Does NOT include exploit-roadmap detail. Strategy
  level only.
- **P0-333-3.** Does NOT auto-merge.
- **P0-333-4.** Does NOT modify code.
- **P0-333-5.** Does NOT propose lowering the ratchet contract's
  monotonicity guarantee (slice 069 invariant).
- **P0-333-6.** Does NOT propose introducing mocks into the
  integration tier (would violate Article IX).
- **P0-333-7.** Does NOT touch CLAUDE.md, canvas.

## Skill mix

- `voltagent-qa-sec:qa-expert` — the named audit agent
- `/idea-to-slice` — for follow-ups
- Standard read/grep — surface enumeration + test-file counting

## Notes for the implementing agent

**Strategy-vs-tactical distinction (load-bearing for AC-3 vs AC-4):**

- **Strategic findings** are about the SHAPE of the QA approach:
  "should we add a contract-test tier?", "is the integration tier the
  right gate vs adding a pre-merge canary deploy?", "should mutation
  testing run weekly?", "is the package-floor coverage model right
  vs branch-floor for security-critical packages?". These warrant
  individual slices because they're load-bearing decisions.
- **Tactical findings** are about SPECIFIC GAPS: "package X has no
  integration tests for endpoint Y", "Playwright spec for tenant-switch
  flow is missing", "flake on chromedp at rate Z%". These bundle into
  "round 1 cleanup" slices because their fix-shape is narrow.

**Flake budget framing suggestion.** A documented flake budget has
three numbers: (1) target flake rate per surface (e.g. <0.5% per
Playwright spec), (2) retry policy (1 retry on flaky failure, then
investigate), (3) flake-debt cap (when total flake rate breaches
target, no new specs land until the debt is paid). The audit
should propose numbers based on observed rates from recent CI runs.

**Cross-reference with slice 334.** This slice is strategy; slice
334 is framework-level. Findings like "the Playwright fixture
discipline is inconsistent" might belong in either — note the
cross-reference and let the maintainer pick ownership.

**Audit log filename:**
`docs/audit-log/333-qa-strategy-gap-analysis-decisions.md`
