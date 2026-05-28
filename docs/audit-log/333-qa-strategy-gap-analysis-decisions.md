# 333 — QA strategy gap analysis decisions log

Slice 333 is `Type: JUDGMENT`. This log records the per-dimension
structured findings per slice doc AC-2 (current-state · target-state ·
gap · remediation path). The audit narrative lives at
`docs/audits/333-qa-strategy-gap-analysis.md`.

Format: Diagnosis · Decision · Revisit-trigger · Confidence.

---

## D1 — Dimension 1: Test pyramid health

| Finding | Current state                                                                                      | Target state                                                                   | Gap                                                                                                              | Remediation path                                                                 |
| ------- | -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Q-1     | 4 surfaces; e2e mocks BFF→atlas wire in 57 places; no contract-test tier between integration + e2e | 5 surfaces; explicit contract-test tier pins HTTP API shapes BFF consumes      | Wire-shape drift between BFF and atlas API not caught by merged gate                                             | Slice 349 (JUDGMENT) — evaluate contract-test tier (golden-file or schemathesis) |
| Q-2     | "Unit tier" mostly handler-level; pure-function base buried in `helpers_test.go` per-package       | Pure-function base documented as canonical path; fast `t.Parallel()` adoption  | Test suite wall-clock scales linearly with feature surface; no fast feedback loop separate from integration tier | Slice 353 (round-1 tactical) — document pure-Go pre-DB convention in CLAUDE.md   |
| Q-3     | Vitest pinned to `node` env; zero React component-level tests; component regressions caught by e2e | Explicit decision: component-test surface (RTL + happy-dom) OR e2e-is-the-tier | Component regressions cost 5s (e2e) instead of 50ms (vitest)                                                     | Slice 353 (round-1 tactical) — propose + decide                                  |

**Decision:** Q-1 → individual JUDGMENT slice; Q-2 + Q-3 → tactical round-1.
**Revisit-trigger:** Q-1 — when first BFF↔atlas wire-shape regression escapes to production; Q-3 — when component-level UI bug count exceeds 3/quarter caught only by e2e.
**Confidence:** Q-1 high (slice 334 P-1 catalogues the supporting evidence); Q-2 medium (sample-based observation); Q-3 high (zero component tests is mechanical).

## D2 — Dimension 2: Coverage strategy

| Finding | Current state                                                                             | Target state                                                                                       | Gap                                                                                               | Remediation path                                                       |
| ------- | ----------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| Q-4     | 197 per-package line-coverage floors; same primitive for security-critical paths and CRUD | Branch-coverage floor (≥90%) for `internal/auth/*`, `internal/api/oauth/*`, `internal/api/authzmw` | Happy-path coverage on auth paths satisfies the line-floor; dangerous error branches sit untested | Slice 350 (AFK) — branch-coverage floor for security-critical packages |
| Q-5     | 61 excludes; growth pattern is monotonic                                                  | Quarterly excludes review with `$justification` field                                              | Excludes are path of least resistance for landing without writing tests                           | Slice 353 (round-1 tactical) — CI lint + quarterly maintenance task    |
| Q-6     | Line coverage measured; assertion density unmeasured                                      | Assertion-density warning (assertions per LOC of test code)                                        | High-line-coverage low-assertion-density tests possible without signal                            | Slice 353 (round-1 tactical) — assertion-density CI step               |

**Decision:** Q-4 → individual AFK slice; Q-5 + Q-6 → tactical round-1.
**Revisit-trigger:** Q-4 — file before next OAuth-touch slice lands; Q-5 — when excludes count > 70; Q-6 — bundled with Q-11 mutation pilot.
**Confidence:** Q-4 high (security-critical paths are well-known); Q-5 medium (growth-rate prediction); Q-6 high (mechanical to measure).

## D3 — Dimension 3: Integration coverage

| Finding | Current state                                                                | Target state                                                                       | Gap                                                                                                                                            | Remediation path                                                                              |
| ------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Q-7     | Hand-maintained CI integration package list; opt-in `//go:build integration` | Either (a) required for DB-touching packages with CI lint, or (b) pragmatic policy | Policy is implicit; 17-slice retroactive enrolment trail (279, 283, 284, 287, 288, 290, 293, 294, 295, 297, 310, 313, 315, 317, 318, 319, 320) | Slice 345 fixes the mechanical side; slice 353 (round-1 tactical) documents the policy choice |
| Q-8     | `-p 1` correct; integration job wall-clock ~10-15min unmeasured              | Wall-clock measured + tracked; trigger at 20min → file Phase A/B split slice       | Future wall-clock problem undefined                                                                                                            | Slice 353 (round-1 tactical) — measure baseline + document the trigger                        |

**Decision:** Q-7 → tactical round-1 (policy + slice 345 already addresses mechanical); Q-8 → tactical round-1.
**Revisit-trigger:** Q-7 — when next post-enrolment slice ships; Q-8 — when integration job exceeds 20 min wall-clock.
**Confidence:** Q-7 high (visible 17-slice trail); Q-8 high (mechanical to measure).

## D4 — Dimension 4: e2e coverage

| Finding | Current state                                                                     | Target state                                                                             | Gap                                                                                                          | Remediation path                                                |
| ------- | --------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------- |
| Q-9     | 47 specs; tenant-switch absent; 8 specs `test.skip`; critical flows half-covered  | Critical multi-tenant flows specced; skip triage with justification or open slice        | Tenant-switch (P0 multi-tenant security flow) has no spec at all; evidence-push-end-to-end only half-covered | Slice 351 (AFK) — audit + file individual specs                 |
| Q-10    | `e2e-audit/` ui-honesty exists, runs against production stack, informational only | Staged promotion: stable assertions move to merged sub-gate; volatile stay informational | Strongest e2e surface in the codebase is NOT on the merged gate                                              | Slice 353 (round-1 tactical) — file staged-promotion path slice |

**Decision:** Q-9 → individual AFK slice; Q-10 → tactical round-1 (path-planning slice).
**Revisit-trigger:** Q-9 — before next multi-tenant slice ships; Q-10 — quarterly review of `e2e-audit/` assertion stability.
**Confidence:** Q-9 high (mechanical grep confirms absence); Q-10 high (`e2e-audit/` exists and is wired into CI as informational).

## D5 — Dimension 5: Mutation testing readiness

| Finding | Current state                                                                             | Target state                                                         | Gap                                           | Remediation path                                                                     |
| ------- | ----------------------------------------------------------------------------------------- | -------------------------------------------------------------------- | --------------------------------------------- | ------------------------------------------------------------------------------------ |
| Q-11    | Zero adoption (`gremlins`, `stryker` absent); readiness high (fast per-package unit tier) | Pilot on one high-value package (`internal/auth/jwt` or `oauthcode`) | Test-suite quality meta-question unaddressed  | NOT in-slice per anti-criteria; future post-Q-15 evaluation slice                    |
| Q-12    | Mutation testing would surface Q-6 (assertion density) as a measurable number             | Q-6 assertion-density step ships as v1 proxy; Q-11 pilot is v3 path  | Asymmetric framing of the same underlying gap | Q-6 (slice 353 round-1) ships v1 proxy; full mutation testing remains a future slice |

**Decision:** No spillover filed per slice doc anti-criteria. Track for post-Q-15 evaluation.
**Revisit-trigger:** When Q-15 (flake budget) lands and surfaces a "is the suite catching regressions vs just lifting numbers?" question.
**Confidence:** Q-11 medium (Go-mutation tooling maturity is improving but not best-in-class); Q-12 high (framing observation).

## D6 — Dimension 6: Defect flow

| Finding | Current state                                                                                                       | Target state                                                                                             | Gap                                                                  | Remediation path                                                 |
| ------- | ------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Q-13    | JUDGMENT-slice decisions logs record root cause; no detection-tier classification                                   | 1-line `detection_tier_actual` + `detection_tier_target` fields per decisions log; quarterly aggregation | Aggregate signal "where bugs are caught vs should be caught" missing | Slice 353 (round-1 tactical) — convention update + template edit |
| Q-14    | Production defect leakage unmeasurable; fix-forward commits informally tracked in `_STATUS.md` UNSTABLE annotations | Formalize `slices_merged_with_fix_forward / total_slices_merged` per quarter                             | OSS-project-with-no-prod defect-leakage metric absent                | Slice 353 (round-1 tactical) — formalize the proxy metric        |

**Decision:** Q-13 + Q-14 → tactical round-1.
**Revisit-trigger:** Q-13 — when first quarterly aggregate runs; Q-14 — when fix-forward rate exceeds 30% of merged slices in a quarter.
**Confidence:** Q-13 high (one-line cost); Q-14 medium (OSS-project metric design has prior art but is not standardized).

## D7 — Dimension 7: Flake economy

| Finding | Current state                                                                                    | Target state                                                                                         | Gap                                                      | Remediation path                                          |
| ------- | ------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------- | -------------------------------------------------------- | --------------------------------------------------------- |
| Q-15    | No flake budget; per-incident judgment; slice 340 / 341 chromedp investigation as exemplar       | Per-surface flake-rate targets + retry policy + investigation triggers + debt cap + weekly dashboard | "Flaky vs hard fail" is judgment; no aggregate watermark | Slice 352 (AFK) — formalize flake budget + ship dashboard |
| Q-16    | Go integration tier no retry policy; Playwright `retries: 1`; tension with slice 322 strict-mode | Explicit decision: integration tier `-retry 1` OR "investigate every flake" written discipline       | Implicit policy means re-litigation per flake            | Slice 353 (round-1 tactical) — decide + document          |

**Decision:** Q-15 → individual AFK slice; Q-16 → tactical round-1.
**Revisit-trigger:** Q-15 — file alongside slice 351 (e2e critical flows) since both surface critical-flow visibility; Q-16 — after Q-15 lands.
**Confidence:** Q-15 high (slice 340 / 341 evidence trail); Q-16 medium (policy choice is contested).

## D8 — Dimension 8: Test-data discipline

| Finding | Current state                                                                                        | Target state                                                                             | Gap                                                                                      | Remediation path                                               |
| ------- | ---------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| Q-17    | 3 test-data realities (Go integration per-package, demoseed global, Playwright per-worker JWT+mocks) | 2 realities: `demoseed.ApplyMinimal()` canonical shared rows; per-package compose on top | Per-package seeds duplicate shared-row INSERTs; the `-p 1` collision surface IS this gap | Slice 353 (round-1 tactical) — demoseed convergence path slice |
| Q-18    | `web/e2e/fixtures.ts` and `web/e2e-audit/fixtures.ts` independent                                    | Converge when Q-10 promotion lands                                                       | Cross-ref to Q-10 only                                                                   | Tracked as Q-10 sub-task; no separate slice                    |

**Decision:** Q-17 → tactical round-1; Q-18 cross-refs Q-10.
**Revisit-trigger:** Q-17 — when integration job wall-clock crosses 20 min (Q-8 trigger); Q-18 — when Q-10 lands.
**Confidence:** Q-17 high (slice 334 `-p 1` review confirms the shared-row surface); Q-18 medium (depends on Q-10 path choice).

---

## Aggregate

| Dimension                     | High | Medium | Low | Spillover destination                       |
| ----------------------------- | ---- | ------ | --- | ------------------------------------------- |
| 1. Test pyramid health        | 1    | 2      | 0   | Q-1 → 349; Q-2/Q-3 → 353                    |
| 2. Coverage strategy          | 1    | 2      | 0   | Q-4 → 350; Q-5/Q-6 → 353                    |
| 3. Integration coverage       | 1    | 1      | 0   | Q-7 → 353 (policy); Q-8 → 353               |
| 4. e2e coverage               | 1    | 1      | 0   | Q-9 → 351; Q-10 → 353 (path-planning)       |
| 5. Mutation testing readiness | 0    | 1      | 1   | Q-11/Q-12 deferred per anti-criteria        |
| 6. Defect flow                | 0    | 1      | 1   | Q-13/Q-14 → 353                             |
| 7. Flake economy              | 1    | 1      | 0   | Q-15 → 352; Q-16 → 353                      |
| 8. Test-data discipline       | 0    | 1      | 1   | Q-17 → 353; Q-18 cross-refs Q-10 (no slice) |

**Total:** 5 High · 10 Medium · 3 Low = 18 findings.

**Spillover cap = 5.** Filed: 349 (Q-1), 350 (Q-4), 351 (Q-9), 352 (Q-15), 353 (tactical round-1 = all medium + low + Q-7 policy + Q-10 path-planning). 5/5 used.

## Cross-references

- Slice 334 audit (`docs/audits/334-test-framework-review.md`) — framework view; this slice is the strategy view
- Slice 347 (vitest ratchet, **merged**) — closes the TS-side measurement asymmetry surfaced as slice 334 V-1
- Slice 069 (verification suite) — coverage ratchet contract that this audit's strategic findings build on
- Slice 340 / 341 (chromedp flake) — canonical recent flake-investigation pattern; cited as the model for Q-15's "investigation trigger"
- Slice 322 (strict-mode discipline) — merge-block contract in tension with Q-16
- Slice 345 (integration enrolment discovery) — fixes the mechanical side of Q-7
- CLAUDE.md "Testing discipline" — four-surface gate that this audit's strategic findings will revise in a future canvas update
