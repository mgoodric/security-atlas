# 348 — Test framework polish round 1

**Cluster:** infra
**Estimate:** 1.5d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Slice 334's framework audit surfaced 20 findings across the four
enforced test frameworks (Go unit, Go integration, vitest, Playwright).
Three high-severity items file as individual slices (345, 346, 347).
The remaining 17 medium and low findings cluster into coherent
sub-themes that bundle into this single polish-round slice per slice
334 AC-4.

**What this slice covers.**

Per the slice 334 audit findings (read
`docs/audits/334-test-framework-review.md` and
`docs/audit-log/334-test-framework-review-decisions.md` first):

### Cluster A — Go unit hygiene

- **U-1.** Sweep pure-unit test files for `t.Parallel()` adoption.
  Target: every test in `internal/eval/state_test.go`,
  `internal/api/oauth/token_test.go`, and any sibling file caught by a
  `grep -L 't\.Parallel' internal/**/*_test.go` (with build-tag
  filtering) gets `t.Parallel()` added unless there is a documented
  reason (shared package-level state).
- **U-4.** Rename `TestRender_ProducesRealPDF_TenIterations` →
  `_FiveIterations` (the name lies; the body runs 5 iterations).
- **U-5.** Document the internal-vs-external test-package convention
  in `CONTRIBUTING.md` (one paragraph: unit tests use `package <pkg>`
  for internal access; integration tests use `package <pkg>_test`).

### Cluster B — Go integration sharpening

- **I-3.** Sharpen the `-p 1` rationale comment in
  `.github/workflows/ci.yml` (line 301-307 area) to:
  1. Name the colliding tables explicitly: `evidence_kind_schemas`,
     `scf_anchors`, `evidence_records`.
  2. Clarify that `-p 1` is NOT primarily about RLS (RLS is
     transaction-scoped via `SET LOCAL`; the colliding rows are
     platform-layer shared seeds + append-only ledger writes, not
     tenant-scoped data).
  3. Reference `docs/audits/334-test-framework-review.md` "-p 1
     rationale review" section.
  4. Name the v3 split-phase relaxation path so the relaxation is
     discoverable when wall-clock pressure shows up.

### Cluster C — vitest hygiene

- **V-2.** Collapse `web/vitest.config.ts` `include` array to a generic
  `**/*.test.ts` glob plus an `exclude` for non-test paths. Preserves
  current coverage; future colocated tests are auto-included.
- **V-3.** Extract the duplicated `NextResponse` mock into
  `web/lib/test-utils/next-mocks.ts`. Sweep the ~40 vitest files that
  re-declare it to import the shared helper.
- **V-4.** Centralize test-bearer literals in
  `web/lib/test-utils/test-tokens.ts`. Sweep `bff.test.ts`,
  `install-state/route.test.ts`, etc. to import. (Honors the slice 069
  P0-A9 / GitGuardian neutral-token policy.)

### Cluster D — Playwright hygiene

- **P-3.** Experiment with `fullyParallel: true` in
  `web/playwright.config.ts`. If 3 consecutive CI runs are green, lift
  the flag (it was justified pre-slice-201 by the static-bearer race).
  If a race surfaces, document the new constraint and revert. Either
  outcome is a successful AC.
- **P-4.** Triage the 8 `test.skip` quarantines:
  `audits-create.spec.ts:42`, `auth-open-redirect.spec.ts:38`,
  `bff-cookie-production-build.spec.ts:61`,
  `logo-render-production-build.spec.ts:47`,
  `questionnaires.spec.ts`, `risks-create-control-link.spec.ts:31`,
  `risks-create.spec.ts:32`, and the documentation comment in
  `control-detail-tabs.spec.ts:34` (resolved by slice 276 but
  comment-laden).
  Each `test.skip` either references an existing env-gate (legitimate)
  or files as its own spillover (test-debt to retire).
- **P-5.** Re-evaluate the chromium-only browser matrix. Document the
  decision (re-confirm or expand). No code change required if
  re-confirmed.

### Cluster E — Tracking items

- **U-2.** Audit the `cmd/scripts/coverage-thresholds.json` `excludes`
  list (60 entries). Categorize each: (a) auto-generated (legitimate,
  keep), (b) integration-tested elsewhere (verify and document),
  (c) unaudited debt (file a coverage-lift slice). This is a
  read-only audit pass producing a categorized table; the actual
  lifts are separate slices.
- **U-3.** Adopt golden-file pattern (`testdata/*.golden.html`) for at
  least the slice-031 brief PDF HTML. Establishes the precedent;
  future templated-render targets can follow.
- **I-4 + I-5.** Document the `internal/testpgx/` helper as a v3 task.
  Full extraction is too large for this slice; the polish round just
  files the tracking ticket.

**Why now:** the audit surfaced these findings; bundling them honors
slice 334 AC-4 (medium + low bundle into one polish slice). Each
cluster is a 1-3 hour chunk; the slice fits a 1.5d budget.

**Trigger:** Surfaced 2026-05-27 by slice 334 framework audit.

## Threat model

Mixed-surface polish slice. STRIDE pass per cluster:

- **Cluster A (Go unit hygiene):** S/T/I/D/E all CLEAN.
- **Cluster B (CI yaml comments):** CLEAN.
- **Cluster C (vitest helpers):** the centralized test-bearer surface
  must keep the slice 069 P0-A9 neutral-token discipline (no
  vendor-prefixed tokens even as placeholders).
- **Cluster D (Playwright):** the `fullyParallel: true` experiment
  must NOT race on the auth fixture's worker-scoped sign-in. Slice 201's
  JWT migration scoped the bearer per worker; the experiment validates
  that the scoping is sufficient.
- **Cluster E (tracking items):** read-only; CLEAN.

## Acceptance criteria

- [ ] **AC-1.** Cluster A (U-1, U-4, U-5) lands.
- [ ] **AC-2.** Cluster B (I-3) lands.
- [ ] **AC-3.** Cluster C (V-2, V-3, V-4) lands.
- [ ] **AC-4.** Cluster D (P-3, P-4, P-5) lands.
- [ ] **AC-5.** Cluster E (U-2, U-3, I-4, I-5) lands as tracking
      documentation (not as full implementation).
- [ ] **AC-6.** No regression in Go unit, Go integration, vitest, or
      Playwright suites — all four CI gates green.
- [ ] **AC-7.** Decisions log written at
      `docs/audit-log/348-test-framework-polish-round-1-decisions.md`
      capturing the JUDGMENT calls (esp. P-3 fullyParallel outcome,
      U-3 golden-file scope, the `test.skip` triage rationale).
- [ ] **AC-8.** `pre-commit run --all-files` passes.

## Constitutional invariants honored

- **Test-First Imperative (Article III).** Every cluster touches the
  test surface; tests must remain green throughout.
- **Integration-First Testing (Article IX).** Cluster D's
  `fullyParallel` experiment must not introduce mocking that didn't
  exist before; the experiment is about parallelism, not mock-vs-real.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — testing discipline
- `CLAUDE.md` "Testing discipline (four enforced surfaces)"

## Dependencies

- **#069** (testing discipline) — `merged`. The framework gate
  this slice polishes.
- **#201** (Playwright JWT migration) — `merged`. The pre-condition for
  Cluster D's `fullyParallel` experiment.
- **#334** (test framework audit) — must be `merged` before this
  slice's implementation starts; the audit defines the findings.

## Anti-criteria (P0 — block merge)

- **P0-348-1.** Does NOT lower any coverage floor.
- **P0-348-2.** Does NOT introduce mocks into the Go integration tier
  (Article IX).
- **P0-348-3.** Does NOT modify the `-p 1` flag in the integration
  job (slice 334 P0-334-3 mirror; the slice only sharpens the
  rationale comment).
- **P0-348-4.** Does NOT touch CLAUDE.md or canvas.
- **P0-348-5.** Does NOT bundle with slices 345 / 346 / 347 — each
  high-severity slice is independent.
- **P0-348-6.** Does NOT keep `fullyParallel: true` if any CI run
  surfaces a real race (document and revert).

## Skill mix

- Go test hygiene (`t.Parallel`, golden files, naming)
- vitest + Playwright config + helper authoring
- yaml editing
- Markdown documentation

## Notes for the implementing agent

The slice has five distinct clusters. Implement them in any order; each
cluster is independently green-CI-able. The recommended order is:

1. Cluster A (cheapest, lowest risk)
2. Cluster B (single-file comment edit)
3. Cluster C (vitest helpers — sweep one finding at a time)
4. Cluster D (Playwright — `fullyParallel` experiment is the
   highest-uncertainty piece)
5. Cluster E (tracking — produces a categorized table for U-2 and a
   v3-tracking note for I-4 / I-5)

Read `docs/audits/334-test-framework-review.md` and
`docs/audit-log/334-test-framework-review-decisions.md` before
starting — they hold the per-finding rationale this slice operationalizes.

For Cluster D (P-3), the experiment shape is:

1. Set `fullyParallel: true` in `web/playwright.config.ts` on a branch.
2. Run `npm run test:e2e` locally 3 times. If all green, push.
3. CI runs 3 PR-update cycles. If all green, the flag lifts.
4. If a race surfaces: capture the trace, document the root cause in
   the decisions log, revert the flag.

This is a JUDGMENT slice — Cluster D's outcome is uncertain by design.
Either outcome is a successful AC if documented in the decisions log.
