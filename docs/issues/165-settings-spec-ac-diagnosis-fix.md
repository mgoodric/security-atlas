# 165 — Diagnose + fix 11 settings.spec.ts AC failures (slice 164 follow-on)

**Cluster:** Quality
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**WHY.** Slice 164 (settings Playwright e2e seed + un-comment AC bodies) merged at `3092f3e` (PR #354) in batch 64 of the 2026-05-19 continuous-loop session. The merge was **UNSTABLE** — the `Frontend · Playwright e2e` CI lane failed with **11 of 132 specs failing**. All 11 failures are in `web/e2e/settings.spec.ts`, and they are precisely the AC bodies slice 164 un-commented: AC-1 through AC-11.

This is a **single-root-cause signature**. Not 11 independent regressions — 11 symptoms of one wiring issue. The failure modes are uniform: `expect(locator).toBeVisible()` finds nothing, or `locator.click` times out at 30s. The page either isn't rendering, isn't authenticated, or doesn't have the data the spec preamble assumes.

Playwright is NOT in `.github/branch-protection.json` required-checks (slice 116 deferred that promotion), so the merge was allowed under the loop's "UNSTABLE = mergeable" rule. The 11 failures became this spillover slice.

**WHAT.** Engineer reproduces locally, reads the trace + screenshot artifacts, picks one of four hypotheses (this is the JUDGMENT call), applies the narrow fix. Likely a 1–5 line change in `fixtures/e2e/settings.sql`, `web/e2e/seed.ts`, or `web/e2e/settings.spec.ts`'s preamble.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT comment-out failing AC bodies. That would defeat slice 164's contract.
- Does NOT add new assertions. Scope creep.
- Does NOT touch slice 162 (sessions wire-shape) or 163 (api tokens rotate) backend code. Their wire shapes are stable post-merge.
- Does NOT promote `Frontend · Playwright e2e` to required-checks. Slice 116 owns that decision.
- Does NOT investigate the other 121 passing specs. They're working.

## Threat model

**Spoofing.** N/A — the slice fixes a test that exists specifically to verify auth behavior; it does not change auth itself.

**Tampering.** N/A — test fixture + spec assertion changes only. No production code path touched.

**Repudiation.** N/A — no audit-log surface.

**Information disclosure.** Indirect risk: if the fix touches `fixtures/e2e/settings.sql`, the engineer must continue to use only synthetic UUIDs (slice-082 convention — tenant `00000000-0000-0000-0000-00000000d3a0`, no real-data identifiers). **Mitigation in this slice:** AC-7 grep-asserts the fixture has no real-data UUIDs post-fix.

**Denial of service.** N/A — single-spec scope; bounded by Playwright's own 30s test timeout.

**Elevation of privilege.** N/A — engineer must NOT alter the harness role binding (slice 082 owns `atlas_app` + RLS context for the e2e fixture path).

**Anti-criteria added from threat model:** P0-A4 (no real-data UUIDs), P0-A5 (no harness role-binding change).

## Diagnosis hypotheses (JUDGMENT — engineer picks ONE in decisions log)

| ID  | Hypothesis                                                                                                                                                                                                                                                             | Likely fix shape                                                                                                                                          | How to confirm                                                                                                                                         |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| H1  | **`seedFromFixture("settings")` helper bug.** Engineer 164's D2 threading added per-fixture `issued_by` resolution. Possible off-by-one or wrong-row map between `FixtureName` enum entry `"settings"` and the corresponding users row in `fixtures/e2e/settings.sql`. | Fix the `web/e2e/seed.ts` map / lookup. 1-3 lines.                                                                                                        | Run spec locally; trace screenshot shows login page (auth failed) OR shows page rendering empty (`/v1/me` returned a different user than the fixture). |
| H2  | **Fixture tenant UUID mismatch.** `fixtures/e2e/settings.sql` seeds rows under a tenant UUID that doesn't match the one `authedPage` fixture authenticates against.                                                                                                    | Fix the `SET LOCAL app.current_tenant = '...'` value in the fixture. 1 line.                                                                              | Compare the fixture's tenant UUID to slice-082's harness tenant (`00000000-0000-0000-0000-00000000d3a0`). If different, that's the bug.                |
| H3  | **Spec preamble drift.** The spec assumes state the fixture doesn't produce — e.g., AC-6 asserts admin cross-link visible "only for admin role" but fixture seeds the bearer's user as `viewer`. ACs 8/10 may have similar preamble drift.                             | Fix the fixture SQL to seed the expected role state, OR update the spec assertion's role-assumption. 1-5 lines.                                           | Read each failing AC's assertion + the fixture's `user_roles` insert. Discrepancy is the bug.                                                          |
| H4  | **`TEST_BEARER` doesn't authenticate the seeded user.** Engineer 164's D2 `issued_by` threading creates an api_key row but `TEST_BEARER` env var resolves to a different `idp_subject`.                                                                                | Fix the bearer-token issuance in `fixtures/e2e/settings.sql` to match what `web/e2e/fixtures.ts` `authedPage` expects (idp_subject + user_id). 1-3 lines. | Trace screenshot will show the `/login` page after the auth step — the bearer didn't authenticate. Cross-reference `web/e2e/fixtures.ts` lines 1-50.   |

**Engineer SHOULD start with H2** (tenant UUID check) — it's the cheapest test (one `grep` on the fixture) and the highest-prior hypothesis given the "all 11 fail uniformly" signature. If H2 is ruled out, proceed to H1 → H4 → H3 in that order.

## Acceptance criteria

**Diagnosis:**

- [ ] AC-1: Reproduce the failure locally. Run `cd web && npx playwright test e2e/settings.spec.ts --headed=false --workers=1` against the docker-compose stack. Confirm 11 settings ACs fail with the same error-modes as the CI artifact at https://github.com/mgoodric/security-atlas/actions/runs/26080968218/job/76682323521.
- [ ] AC-2: Identify the hypothesis (H1/H2/H3/H4) via trace screenshot + error-context.md inspection. Record evidence in the decisions log.

**Fix:**

- [ ] AC-3: Apply the narrow fix matching the chosen hypothesis. Diff is < 20 lines across `fixtures/e2e/settings.sql`, `web/e2e/seed.ts`, or `web/e2e/settings.spec.ts` preamble ONLY. Do NOT touch other fixtures, other specs, or any production code.

**Verification:**

- [ ] AC-4: All 11 previously-failing settings ACs pass locally. `npx playwright test e2e/settings.spec.ts --workers=1` reports 11 newly-passing.
- [ ] AC-5: The 121 previously-passing specs still pass. Full `npx playwright test --workers=1` reports zero new failures.
- [ ] AC-6: CI `Frontend · Playwright e2e` job on the PR completes with 0 settings-spec failures.

**Threat-model mitigations:**

- [ ] AC-7 (from threat model — Disclosure): Verify `fixtures/e2e/settings.sql` post-fix uses only synthetic UUIDs. Grep for real-data patterns: no real tenant slugs, no real user emails, no UUIDs not matching `00000000-0000-0000-0000-*` or `33333333-*` pattern.
- [ ] AC-8 (from threat model — EoP): Confirm post-fix fixture has zero `SET ROLE`, `SET SESSION AUTHORIZATION`, or `\connect` statements. Harness role binding stays slice-082 owned.

**Decisions log:**

- [ ] AC-9: `docs/audit-log/165-settings-spec-ac-diagnosis-fix-decisions.md` records (a) which hypothesis won + evidence; (b) alternatives ruled out + how; (c) confidence per decision; (d) revisit-once-in-use list (e.g., "if slice 162's wire shape changes, re-verify AC-5 fixture row for sessions still matches").

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only."** Each hypothesis dictates the minimum scope. The slice does NOT comment-out failing assertions (defeats slice 164's contract) and does NOT refactor adjacent code.
- **CLAUDE.md "Never assert without verification."** AC-1 (local repro) + AC-4 (local pass) + AC-5 (no regression on other specs) + AC-6 (CI green) are all evidence-required.
- **CLAUDE.md "Fail-fast after two failed attempts."** If hypothesis #2 also doesn't pan out, the engineer should escalate rather than guess hypothesis #3 — the test failure signature should clearly discriminate between H1-H4.
- **Slice 164's contract.** All 11 un-commented assertions land in passing state; the un-comment is not reverted.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Playwright + e2e testing posture
- Slice 082 (`082-playwright-seed-data-harness.md`) — harness contract (`seedFromFixture`, `authedPage`, `TEST_BEARER`)
- Slice 154 (`154-settings-page-audit-and-parity-check.md`) — F11 parent finding
- Slice 162 (`162-sessions-wire-shape-augment-ua-ip-geo.md`) — sessions wire shape (relevant for AC-5)
- Slice 163 (`163-settings-api-tokens-rotate-action.md`) — token rotate (relevant for AC-4, AC-11)
- Slice 164 (`164-settings-e2e-seed-fixture-and-uncomment.md`) — direct parent; the slice this fix follows
- `web/e2e/seed.ts` — harness loader
- `web/e2e/fixtures.ts` — `authedPage` fixture + `TEST_BEARER` consumer
- `fixtures/e2e/settings.sql` — the fixture this slice might fix

## Dependencies

- #164 — merged. Direct parent slice; the un-comment this PR makes work.
- #082 — merged. Harness ownership.
- #162 — merged. Sessions wire shape (AC-5).
- #163 — merged. Token rotate (AC-4, AC-11).

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT comment-out any failing AC body. That would defeat slice 164's contract. The slice's whole point is to make all 11 ACs pass un-commented.
- **P0-A2**: Does NOT add new assertions to settings.spec.ts. Scope creep.
- **P0-A3**: Does NOT modify `internal/api/me/*.go`, `internal/auth/sessions/*.go`, or any production handler. The wire shapes for slices 108/162 are stable and tested; the bug is in the test harness or fixture, not the runtime.
- **P0-A4** (from threat model — Disclosure): Does NOT introduce real-data UUIDs or real-tenant slugs in `fixtures/e2e/settings.sql`. AC-7 verifies.
- **P0-A5** (from threat model — EoP): Does NOT add `SET ROLE` / `SET SESSION AUTHORIZATION` / `\connect` to the fixture. Harness owns role binding. AC-8 verifies.
- **P0-A6**: Does NOT promote `Frontend · Playwright e2e` to required-checks. Slice 116 owns that promotion gate.
- **P0-A7**: Does NOT use vendor-prefixed test fixture tokens (carry-over convention from slice 05).
- **P0-A8**: Does NOT regress the 121 currently-passing specs. AC-5 verifies.

## Skill mix

- Playwright trace + screenshot diagnosis (`npx playwright show-trace test-results/.../trace.zip` is the canonical tool)
- `fixtures/e2e/*.sql` schema understanding (slice 082 + slice 164 fixtures as references)
- `web/e2e/seed.ts` + `web/e2e/fixtures.ts` reading
- Decisions-log discipline (slice 109 / 152 / 159 / 161 / 163 are recent JUDGMENT-slice examples)

## Notes for the implementing agent

**Provenance:** Surfaced 2026-05-19 during the slice 164 PR session (continuous-loop batch 64). Slice 164's engineer stalled mid-simplify-pass; orchestrator close-out shipped the slice with the 11 Playwright failures unresolved. Full escalation context at `~/.claude/MEMORY/STATE/continuous-batch-escalation.md` if needed.

**The single-root-cause signature is the key insight.** 11 ACs failing independently would be 11 separate bugs requiring 11 separate fixes. 11 ACs failing uniformly (locator-not-found / 30s-timeout) means the page either isn't rendering or isn't authenticated correctly. A 1-5 line fix should resolve all 11.

**Triage order (cost-ordered):**

1. **Grep test (1 minute):** Compare `SET LOCAL app.current_tenant = ...` in `fixtures/e2e/settings.sql` to the slice-082 harness tenant `00000000-0000-0000-0000-00000000d3a0`. If different, fix that one line and re-run. (H2)
2. **Local repro + trace inspection (5-10 minutes):** Run the spec, open `test-results/settings-*/test-failed-1.png`. The screenshot tells you whether the page is on `/login` (auth failed → H4), blank (fixture didn't seed → H2/H3), or partially-rendered (wrong role → H3).
3. **Map verification (5 minutes):** Read `web/e2e/seed.ts` `FixtureName` enum + the `issued_by` map. Confirm `"settings"` maps to the user row that `fixtures/e2e/settings.sql` actually inserts. (H1)
4. **Bearer verification (10 minutes):** Read `web/e2e/fixtures.ts` `authedPage` fixture. Confirm `TEST_BEARER` env var's expected `idp_subject` matches what `fixtures/e2e/settings.sql` seeds. (H4)

**Don't escalate prematurely.** The fail-fast rule says escalate after two failed hypotheses, not after the first ambiguous trace screenshot. Take H2 (cheapest) first, then H1 if H2 doesn't pan out. If both rule out, then escalate with evidence.

**Cross-link:** When this slice merges, update the slice 164 \_STATUS row's "SPILLOVER: file slice 165" note to mark resolved. The reconcile drift block can also be updated to note batch-64-stalled-but-batch-65-fixed.
