# 193 — Diagnose + fix dashboard.spec.ts AC-5 upcoming-row Playwright failure (slice 111 spillover)

**Cluster:** Quality
**Estimate:** 0.5d
**Type:** JUDGMENT (diagnose-heavy; root-cause unknown)
**Status:** `ready` (slice 111's un-skip caught the failure; spillover-as-slice per continuous-batch policy)

## Narrative

Surfaced during slice 111 (enable full assertions in `dashboard.spec.ts`), captured as follow-up per continuous-batch policy.

Slice 111 un-skipped 11 dashboard test bodies that had been commented out since slice 082. The first post-rebase CI run on PR #441 surfaced a real failure: AC-5 ("upcoming panel binds to `/v1/upcoming` unified rollup (slice 157)") fails because `page.getByTestId("upcoming-row").first()` is not visible within the 5-second timeout.

Failure URL (slice 111's PR #441 run 26234782812):
`https://github.com/mgoodric/security-atlas/actions/runs/26234782812/job/77205003316`

Failure shape:

```
e2e/dashboard.spec.ts:120:7 › dashboard view › AC-5: upcoming panel binds to /v1/upcoming unified rollup (slice 157)
  Error: expect(locator).toBeVisible() failed
  Expected: visible
  - Expect "toBeVisible" with timeout 5000ms
  > 137 |     await expect(page.getByTestId("upcoming-row").first()).toBeVisible();
```

Two non-mutually-exclusive root-cause hypotheses:

**H1 — Fixture gap.** Slice 082's `fixtures/e2e/dashboard.sql` was declared FULL but may not include rows that the slice-157 `/v1/upcoming` unified-rollup endpoint surfaces as "upcoming". Slice 157 changed the dashboard data path to unify policy/exception/control events into a single endpoint — the fixture may seed the underlying tables (policies, exceptions, controls) but not in a way that produces non-empty `/v1/upcoming` output (e.g., dates in the past, wrong status states).

**H2 — Spec drift on testid.** The assertion uses `data-testid="upcoming-row"`. The dashboard's upcoming panel may render rows with a different testid (e.g., `upcoming-event-row`, `upcoming-item`) after slice 157's refactor.

**Investigation steps for the engineer:**

1. Read the failure screenshot in the run artifacts (`test-results/dashboard-dashboard-view-A-a6416-g-unified-rollup-slice-157--chromium/test-failed-1.png`).
2. Read `web/components/dashboard/upcoming-panel.tsx` (or wherever the upcoming panel is rendered) — confirm the actual `data-testid` on the row elements.
3. Read `fixtures/e2e/dashboard.sql` — confirm whether it seeds rows that the `/v1/upcoming` endpoint surfaces given today's date.
4. Diagnose H1 vs H2. Fix accordingly:
   - If H1: extend `dashboard.sql` with seed rows + relative-date math (or fixed seed dates known to be "upcoming" relative to a frozen test clock).
   - If H2: update the assertion's `getByTestId` argument to match the actual rendered testid.
5. Run `cd web && npm run test:e2e -- dashboard.spec.ts` locally to verify green.
6. If the dashboard.spec has OTHER failures beyond AC-5, address them in this slice OR file additional spillovers.

**What this slice ships:**

- Diagnosis verdict in decisions log (H1 vs H2 vs both)
- The actual fix (fixture extension OR assertion correction OR both)
- Local-run + CI verification

**SCOPE DISCIPLINE — what's deliberately out:**

- The settings.spec.ts failures (AC-5/AC-7/AC-9) that landed alongside this dashboard failure. Those are pre-existing flakes unrelated to slice 111's surface; if they need fixes, file separate spillovers per Amendment 2.
- Promoting `Frontend · Playwright e2e` to required-checks. That's slice 116.
- Extending dashboard.sql with non-upcoming-related seed data. Stay focused on the AC-5 failure surface.

## Threat model

Pure test-fixture / test-spec work. No security surface. **Verdict: clean.**

## Acceptance criteria

- **AC-1.** Read the slice 111 CI run's failure artifacts to identify the actual rendered testid + seed gap.
- **AC-2.** Decisions log at `docs/audit-log/193-dashboard-upcoming-fixture-decisions.md` captures: H1 vs H2 verdict, the fix chosen, why.
- **AC-3.** Either `fixtures/e2e/dashboard.sql` updated with upcoming-event seed rows OR `web/e2e/dashboard.spec.ts:137` assertion's testid updated to match the actual rendered DOM.
- **AC-4.** `cd web && npm run test:e2e -- dashboard.spec.ts` PASSES locally against a fresh seed.
- **AC-5.** CI run on the slice 193 PR: `Frontend · Playwright e2e` for `dashboard.spec.ts` PASSES.
- **AC-6.** `_STATUS.md` row flipped to `merged` post-CI-green.

## Constitutional invariants honored

None directly applicable — this is test-harness work.

## Canvas references

- Slice 111 (parent): un-skipped dashboard.spec assertions; CI surfaced this failure.
- Slice 082: original seed harness + FULL dashboard.sql declaration.
- Slice 157: unified `/v1/upcoming` rollup endpoint refactor.

## Dependencies

- **#111** (merged) — this slice fixes the failure 111 surfaced.
- **#082** (merged) — fixture infrastructure.
- **#157** (merged) — endpoint that the test asserts against.

## Anti-criteria (P0 — block merge)

- **P0-193-1.** Does NOT relax the assertion to be permissive (e.g., changing `toBeVisible` to `count >= 0`). The whole point of slice 111 was to ENABLE strict assertions — relaxing them defeats slice 111's purpose.
- **P0-193-2.** Does NOT comment out the failing assertion. If the assertion can't be made green, return to caller with the design question (the test was written against an expected behavior; if that behavior changed, the canvas or another slice needs to address it).
- **P0-193-3.** Does NOT modify settings.spec.ts. The settings failures are unrelated; if they need attention, file additional spillover slices.

## Skill mix (3-5)

- `grill-with-docs`
- `tdd` (or just test-debugging — read source, run locally, iterate)
- `simplify`
- `ship-gate`

## Notes for the implementing agent

### The seed/wire gap is what slice 111 was DESIGNED to catch

This is a feature, not a regression. Slice 082 declared `dashboard.sql` FULL but the assertions were commented; nobody verified the contract end-to-end. Slice 111 un-skipped → slice 193 fixes what un-skip surfaced. This is the spillover pattern working as intended.

### Compare against slice 168/170/171 chain

That chain caught 4 settings.spec failures from slice 165's un-skip and addressed each one as a separate slice. Same shape here.

### Provenance

Filed 2026-05-21 as orchestrator-driven spillover from slice 111 first-pass CI failure. The slice-111 engineer didn't file this themselves because they didn't run the tests locally before push.
