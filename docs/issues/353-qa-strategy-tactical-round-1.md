# 353 — QA strategy tactical round 1

**Cluster:** Quality
**Estimate:** 2-3d (multi-finding bundle)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 333's QA strategy audit
(`docs/audits/333-qa-strategy-gap-analysis.md`) surfaced 18 findings
across 8 dimensions. The 4 high-severity strategic findings file as
individual slices (349, 350, 351, 352). The remaining medium and low
findings bundle into this tactical round-1 slice per slice 333
AC-4: their individual fix-shapes are narrow and they cluster into
sub-themes that can be addressed coherently.

### Findings bundled

#### Sub-theme A — Documentation + convention edits (CLAUDE.md / canvas)

- **Q-2 — Pure-Go pre-DB convention.** Document the slice 290 /
  297 / 310 / 315 / 320 pattern (pure-Go helpers_test.go alongside
  integration tests) in CLAUDE.md "Testing discipline" or
  `Plans/canvas/09-tech-stack.md`. The pattern is established;
  formalize it.
- **Q-3 — Component-test surface decision.** Decide explicitly:
  (a) add a vitest `happy-dom` project for React component-level
  testing, or (b) document that the e2e tier is the de facto
  component-test tier and component-level vitest is OUT of scope.
  Pick one; document in CLAUDE.md.
- **Q-7 — Integration enrolment policy.** Slice 345 closes the
  mechanical discovery side; this slice documents the policy:
  "every package importing `internal/db/dbx` or setting
  `app.current_tenant` SHOULD ship an `integration_test.go`" with
  the discovery primitive as the enforcement mechanism. Update
  CLAUDE.md.
- **Q-16 — Integration tier retry policy.** Decide: `-retry 1`
  for the Go integration tier OR explicit "investigate every flake"
  written discipline. Document the choice in CLAUDE.md.

#### Sub-theme B — Maintenance task creation

- **Q-5 — Coverage excludes maintenance.** Add a quarterly maintenance
  task (not a slice; a maintainer review) that examines each
  exclude in `coverage-thresholds.json`, either justifies it
  in-place with a `$justification` comment, or files a slice to
  retire it. Add a CI lint that asserts every exclude has a
  `$justification` field.
- **Q-8 — Integration job wall-clock measurement.** Add a CI step
  that records the integration job's wall-clock on every clean
  `main` run; emit a watermark file. Document the trigger: when
  wall-clock exceeds 20 min, file the Phase A/B split slice.

#### Sub-theme C — Assertion / defect signal

- **Q-6 — Assertion-density CI step.** Add a CI step that counts
  `t.Errorf`/`t.Fatalf`/`require.*`/`assert.*` calls per test file
  and emits a warning if assertion density is below a threshold
  (e.g. <1 per 20 LOC of test code). Cheap mutation-testing proxy.
- **Q-13 — Detection-tier classification in decisions logs.** Add
  two fields to JUDGMENT-slice decisions logs:
  `detection_tier_actual` and `detection_tier_target`. Update the
  template at `Plans/prompts/04-per-slice-template.md` (or wherever
  the JUDGMENT-slice decisions-log template lives). Document in
  CLAUDE.md.
- **Q-14 — Fix-forward rate metric.** Formalize
  `slices_merged_with_fix_forward / total_slices_merged` per
  quarter. Surface in `_STATUS.md` or a sidecar file.

#### Sub-theme D — Test-data convergence path

- **Q-17 — Demoseed convergence path slice.** File a follow-on
  slice (not in this round) that designs `demoseed.ApplyMinimal()`
  and migrates per-package integration-test seeds onto it. This
  slice's job is to scope the convergence — produce a design doc
  at `docs/adr/00NN-demoseed-convergence.md` outlining (a) which
  per-package seeds duplicate shared rows, (b) the two-cycle
  migration plan, (c) the `-p 1` collision surface mapping. Do
  NOT migrate seeds in this slice.

#### Sub-theme E — ui-honesty promotion path

- **Q-10 — `e2e-audit/` promotion path.** File a path-planning doc
  at `docs/adr/00NN-ui-honesty-promotion-path.md` outlining the
  staged promotion: (a) audit current `ui-honesty.spec.ts`
  assertions over 30 days for stability; (b) move stable assertions
  to a merged sub-gate; (c) keep volatile ones informational;
  (d) migration plan from `e2e/` mocked specs to `e2e-audit/` real
  specs over N slices. Do NOT promote in this slice — the slice
  ships the plan, future slices execute.

### What this slice does NOT do

- Does NOT fix mutation-testing readiness (Q-11 / Q-12) — deferred
  per slice 333 anti-criteria.
- Does NOT fix Q-18 — folded into Q-10's promotion path.
- Does NOT execute the demoseed convergence (Q-17) — only scopes it.
- Does NOT execute the ui-honesty promotion (Q-10) — only scopes it.

## Threat model

Documentation + convention + CI-tooling edits only. No runtime
code. STRIDE pass:

- **I (information disclosure):** Sub-theme C's detection-tier
  fields capture WHERE bugs were caught. **Mitigation:** the
  decisions logs are repo-internal; same access control as
  slice 333.
- Others: CLEAN.

## Acceptance criteria

- [ ] **AC-1.** CLAUDE.md "Testing discipline" updated per
      sub-themes A.Q-2, A.Q-3, A.Q-7, A.Q-16.
- [ ] **AC-2.** `coverage-thresholds.json` excludes updated per
      sub-theme B.Q-5: each exclude has `$justification`. CI lint
      added.
- [ ] **AC-3.** Integration job wall-clock measurement step added
      per sub-theme B.Q-8.
- [ ] **AC-4.** Assertion-density CI step added per sub-theme
      C.Q-6.
- [ ] **AC-5.** JUDGMENT-slice decisions-log template updated
      per sub-theme C.Q-13.
- [ ] **AC-6.** Fix-forward rate metric formalized per sub-theme
      C.Q-14.
- [ ] **AC-7.** Demoseed convergence design doc at
      `docs/adr/00NN-demoseed-convergence.md` per sub-theme D.
- [ ] **AC-8.** ui-honesty promotion path doc at
      `docs/adr/00NN-ui-honesty-promotion-path.md` per sub-theme E.
- [ ] **AC-9.** Decisions log at
      `docs/audit-log/353-qa-strategy-tactical-round-1-decisions.md`
      capturing per-finding outcomes.
- [ ] **AC-10.** Cross-references slice 333 (all bundled findings)
      and slice 334 (overlapping framework findings).
- [ ] **AC-11.** `pre-commit run --files` passes.

## Anti-criteria

- **P0-1.** Does NOT execute the demoseed convergence migration —
  only scopes it (sub-theme D).
- **P0-2.** Does NOT execute the ui-honesty promotion — only
  scopes it (sub-theme E).
- **P0-3.** Does NOT add mocks to the integration tier (would
  violate Article IX).
- **P0-4.** Does NOT lower any coverage floor.
- **P0-5.** Does NOT modify slice 333's audit findings — the audit
  is the input contract; this slice executes against it.

## Dependencies

- **#333** (QA strategy audit) — `merged`. The input contract.
- **#334** (test framework review) — `merged`. Overlapping findings
  cited per AC-10.
- **#345** (CI integration job enrolment discovery) — should
  ship before A.Q-7's policy documentation lands.

## Notes for the implementing agent

This is a multi-finding bundle. The right shape is:

1. Land sub-theme A first — pure documentation edits, no risk.
2. Land sub-theme B second — coverage excludes + wall-clock
   measurement. Touches CI but mechanically.
3. Land sub-themes C / D / E in any order — they're independent.

Each sub-theme can land as a separate commit within the slice if
the implementer prefers. The slice does not have to land as a
single squash; commits-then-squash is fine. Decisions log captures
the per-finding outcome.

If any sub-theme is more than 1d of work, split it into a
follow-up slice and document the split in the decisions log.
