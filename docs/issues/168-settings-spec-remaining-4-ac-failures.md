# 168 — Diagnose + fix remaining 4 settings.spec.ts AC failures (slice 165 follow-on)

**Cluster:** Quality
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**WHY.** Slice 165 (`fix(e2e): slice 165 — diagnose + fix 11 settings.spec.ts AC failures`) shipped over two PRs across batch 65:

- **Iter 1** (PR #358 squash-merged at `ed4d1e1`): engineer's `authedPage` fixture rebind. Flipped 1/11 ACs red → green (AC-6 admin cross-link).
- **Iter 2** (PR #360 squash-merged at `e725893`): engineer's `allowed_kinds` fixture workaround for a production null-deref crash (filed as spillover slice 166). Flipped 6 more ACs red → green (AC-5, AC-7, AC-8, AC-9, AC-10, AC-11).

**Net: 7/11 settings ACs now pass in CI.** Four remain red:

| AC   | Title                                                                 | Line   | Failure mode                                                                           |
| ---- | --------------------------------------------------------------------- | ------ | -------------------------------------------------------------------------------------- |
| AC-1 | profile section renders for any signed-in user                        | 44-50  | `getByRole("heading", { name: /Profile/ })` toBeVisible 5s timeout — element not found |
| AC-2 | theme picker persists choice across reload                            | 52-59  | `locator.click(getByTestId("settings-theme-option-dark"))` 30s timeout                 |
| AC-3 | notification toggle persists server-side across reload (slice 164 D1) | 63-90  | `page.waitForResponse(/v1/me/preferences)` 30s timeout                                 |
| AC-4 | token issuance shows plaintext once then never re-displays it         | 93-118 | `locator.click(getByTestId("settings-token-issue-button"))` 30s timeout                |

**Critically: this is NOT a single-root-cause failure** (unlike slice 165's iter 1 surface). Each of the 4 ACs has its own probable cause. The "all 11 share one bug" pattern slice 165 confronted was the `allowed_kinds`-null crash that unmounted the page subtree. With iter 2 in place, the page now renders far enough that 7 ACs find their elements; the remaining 4 fail for individual reasons.

**WHAT.** Engineer triages the 4 ACs INDEPENDENTLY:

- For each AC, reproduce locally (or read CI artifact trace + screenshots).
- Identify the specific cause per AC (4 hypotheses below, one per AC — engineer can confirm/refute each).
- Apply a narrow fix scoped to fixture / seed / spec preamble (P0-A3 still binds: no production code).
- Decision-log each diagnosis + fix with its own subsection.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT re-investigate the 7 ACs that are already green. They pass.
- Does NOT touch production code at `web/app/(authed)/settings/page.tsx`, `internal/api/`, or `web/components/`. P0-A3 inherited from slice 165.
- Does NOT add or remove AC bodies in `web/e2e/settings.spec.ts`. Engineer may correct broken locators / assertions ONLY if the spec's intent is clearly preserved.
- Does NOT modify slice 162 (sessions wire shape) or 163 (api tokens rotate) backends.
- Does NOT promote `Frontend · Playwright e2e` to required-checks.
- Does NOT bundle the slice 166 production fix (`allowed_kinds` null marshal) into this work. Slice 166 is its own slot.
- Does NOT investigate test infrastructure not directly related to these 4 ACs.

## Threat model

Same surface as slice 165 (test fixture + spec modifications; no production runtime change). STRIDE pass below for completeness.

**S — Spoofing.** Inherited: spec changes don't add new auth surface. No new threat.

**T — Tampering.** Spec/fixture-only changes. The risk is testing-domain only: a brittle fix that masks a real production bug rather than reproducing it. Mitigation: every fix in this slice MUST identify whether it's exposing a real test-infrastructure bug, a real production bug (file as spillover), or a genuine spec drift; the decisions log records this for each AC.

**Anti-criterion P0-A1:** Engineer MUST classify each fix as (a) test-infra gap (b) spec drift (c) production bug (file spillover). No "just make it pass" fixes without classification.

**R — Repudiation.** No audit-log writes. No threat.

**I — Information disclosure.** Inherited: fixture data uses neutral test tokens / synthetic UUIDs. No threat.

**D — Denial of service.** Inherited from slice 165. No threat.

**E — Elevation of privilege.** Inherited from slice 165. No threat.

**Verdict.** has-mitigations (T produces a real anti-criterion around fix classification).

## Hypotheses to investigate (one per AC)

### AC-1 — profile section heading not visible

Spec asserts BOTH `getByTestId("settings-section-profile")` visible AND `getByRole("heading", { name: /Profile/ })` visible. AC-8 (time-zone select INSIDE profile section) passes, which means `settings-section-profile` IS rendering. Therefore the failing assertion is the heading regex match.

**Hypothesis A1-H1 (cheapest):** the rendered heading text doesn't match `/Profile/`. Maybe it says "Your Profile", "Account", "Personal Settings", or it's an icon-only heading (no text). Check `web/app/(authed)/settings/page.tsx` lines 237-260 for the actual heading.

**Hypothesis A1-H2:** the heading uses a non-heading role (e.g., `<div>` styled as heading; `<h2>` inside a `<Card>` but the `<Card>` injects its own role). Run `page.locator('h1,h2,h3,h4,h5,h6')` filter and dump what's actually there.

**Hypothesis A1-H3:** the heading text IS "Profile" but the Card renders it via `<CardTitle>` which is `slot="title"` in shadcn, NOT a heading element. Playwright's `getByRole("heading")` misses it.

**Recommended fix:** if A1-H1 / A1-H3 — adjust the spec's `getByRole` query to match the actual rendered element (e.g., `getByTestId("settings-profile-display-name")` or `getByText(/Profile/)` instead of `getByRole("heading", { name: /Profile/ })`). 1-2 line change in spec.

### AC-2 — theme picker option-dark click timeout

Spec calls `page.getByTestId("settings-theme-option-dark").click()` at line 56. Times out at 30s.

**Hypothesis A2-H1 (cheapest):** the testid `settings-theme-option-dark` doesn't exist on the rendered element. Grep `web/app/(authed)/settings/page.tsx` lines 451-590 (Appearance Card) for the actual testid scheme.

**Hypothesis A2-H2:** the testid exists but the element is `disabled` or `pointer-events: none` until some condition resolves (e.g., user preferences load). Inspect the trace to see if the element is present-but-not-clickable.

**Hypothesis A2-H3:** the testid exists, the element is clickable, but the click is racing against the page hydration. The spec needs `await page.waitForLoadState("networkidle")` before the click, OR the theme picker is wrapped in a hydration-deferred component.

**Recommended fix:** if A2-H1 — fix the testid in the spec OR in the SettingsClient (but if it's the SettingsClient, P0-A3 forces a slice 169 spillover for the production fix; the spec stays broken until then). If A2-H2 — extend the spec preamble to wait for the precondition. If A2-H3 — add an explicit hydration wait.

### AC-3 — notifications PATCH waitForResponse timeout

Spec at line 63-90 toggles a notification preference, clicks it, then `await page.waitForResponse(/me\/preferences/)`. The response never comes within 30s.

**Hypothesis A3-H1:** the toggle click doesn't actually fire a PATCH — the click target is wrong OR the toggle is mocked-out OR optimistic-update is configured to not roundtrip. Inspect the network panel of the failing trace.

**Hypothesis A3-H2:** the PATCH IS fired but to a different URL than the spec's regex matches (`/v1/me/preferences/email` vs `/v1/me/preferences` etc.). Adjust the regex.

**Hypothesis A3-H3:** the PATCH IS fired correctly but returns 4xx/5xx (auth, validation, or RLS issue). The 5xx wouldn't cause `waitForResponse` to time out — it would resolve. So this is less likely.

**Hypothesis A3-H4:** the click target's `data-testid` doesn't match what the spec selects. Engineer grep the spec for the toggle locator + page.tsx Notifications Card (lines 591-760) for the actual rendered testid.

**Recommended fix:** narrow per the hypothesis. Most likely A3-H2 (URL pattern drift) — 1-line regex fix in spec.

### AC-4 — token issuance click timeout

Spec at line 93-118 clicks `getByTestId("settings-token-issue-button")` and asserts the plaintext modal appears.

**Hypothesis A4-H1 (cheapest):** the testid exists on a DIFFERENT element than the spec expects. Grep page.tsx lines 787-870 for `settings-token-issue-button` and check render conditions. The fixture seeds an admin user; only admins see the Issue button (line 787 vs 760 — there's an admin-vs-non-admin split). If the fixture's user isn't admin in the right way, the button doesn't render.

**Hypothesis A4-H2:** the button exists but is `disabled` while the credentials list loads. Inspect trace.

**Hypothesis A4-H3:** the button click fires but the resulting modal renders to a different testid than the spec asserts. The plaintext assertion is what times out, not the click itself.

**Hypothesis A4-H4:** the issue-token API call fails (the credentials list works post-iter-2 fix, but ISSUE is a different endpoint that may have its own bug). Spillover candidate.

**Recommended fix:** narrow per hypothesis. If A4-H4 — file slice 169 (production bug) and skip the AC for now, OR find a fixture workaround.

## Acceptance criteria

### Diagnosis

- **AC-1.** Each of the 4 failing ACs (settings-spec AC-1, AC-2, AC-3, AC-4) gets its own diagnosis section in `docs/audit-log/168-settings-spec-remaining-4-decisions.md`, including: which hypothesis (per the lists above) matched, what evidence (grep / trace / screenshot), and which narrow fix was applied.

- **AC-2.** For ACs where the cause is a production bug (testid drift in `page.tsx`, conditional render bug, missing API endpoint, etc.), engineer files a SPILLOVER slice (169 etc.) per the spillover-as-slice policy. The spillover's narrative cites slice 168 as parent.

### Fix

- **AC-3.** Apply narrow fixes (1-5 lines each) in `web/e2e/settings.spec.ts` preamble / locators, `fixtures/e2e/settings.sql`, `web/e2e/seed.ts`, OR (only with explicit decision-log justification) `web/app/(authed)/settings/page.tsx` testid attributes — IF the production source is the bug and a 1-line testid addition is the right fix (this is the ONE permitted exception to P0-A3; document the call).

- **AC-4.** Total substantive line changes across all 4 AC fixes ≤ 20. If you're rewriting more than that, you're probably bundling scope; stop and re-scope.

### Verification

- **AC-5.** Reproduce CI's Playwright failure locally before applying the fix (engineer runs docker-compose + `npm run test:e2e -- --grep "settings"` or equivalent). Decision log captures the local repro output.

- **AC-6.** All 4 of the previously-failing settings.spec.ts ACs PASS in CI Playwright after the fix. Net result: 11/11 settings ACs green in CI.

- **AC-7.** No regression in the 7 previously-passing settings ACs (AC-5, AC-6, AC-7, AC-8, AC-9, AC-10, AC-11).

- **AC-8.** No regression in the other 121 specs.

### Documentation

- **AC-9.** Decisions log at `docs/audit-log/168-settings-spec-remaining-4-decisions.md` covers:

  - One section per AC with hypothesis match, evidence, fix description (1-2 paragraphs)
  - Classification per anti-criterion P0-A1: test-infra gap / spec drift / production bug
  - Spillover slices filed (if any) with their slot numbers

- **AC-10.** If a production fix is applied per AC-3 (the P0-A3 exception), the decision log captures: which testid was added/corrected, why a fixture workaround wasn't viable, why the fix is "1-line testid attribute" and not more.

## Constitutional invariants honored

- **Slice 165's contract**: this slice completes 165's "11/11 ACs pass" goal across two follow-on PRs (165 itself shipped 7/11; 168 closes the gap).
- **Slice 075's integration contract**: the logo and other mount points are untouched.
- **Test discipline**: no commented-out AC bodies; no broad rewrites; per-AC narrow fixes.
- **Spillover-as-slice**: production bugs surface as their own slots, NOT bundled.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — settings page IS a major surface for the operator (per slice 154 audit).
- `Plans/canvas/09-tech-stack.md` — Playwright e2e discipline (advisory now, gating later per slice 116).

## Dependencies

- **#165** (slice 165 follow-on) — `merged` at e725893 (iter 2). This slice completes 165's contract.
- **#166** (allowed_kinds null marshal) — `ready`, parallel. The 168 work assumes 166's fixture workaround is in place (it is, post-165 iter 2).

## Anti-criteria (P0 — block merge)

- **P0-A1.** Engineer MUST classify each fix as (a) test-infra gap (b) spec drift (c) production bug (file spillover). No undocumented "just make it pass" fixes.
- **P0-A2.** Does NOT comment out failing AC bodies. Inherited from slice 165 P0-A1.
- **P0-A3.** Does NOT add new assertions to settings.spec.ts. Locator corrections are permitted ONLY if the spec's intent is preserved; new assertions are scope creep.
- **P0-A4.** Does NOT touch production code beyond at most ONE testid attribute addition per AC, only if documented per AC-10. Inherited from slice 165 P0-A3 with the documented carve-out.
- **P0-A5.** Does NOT modify slice 162 (sessions wire shape) or 163 (api tokens) backend wire shapes.
- **P0-A6.** Does NOT promote `Frontend · Playwright e2e` to required-checks. Slice 116 owns that decision.
- **P0-A7.** Does NOT introduce real-data UUIDs in fixtures. Neutral test tokens only.
- **P0-A8.** Does NOT change the test harness role binding. Inherited.
- **P0-A9.** Does NOT bundle the slice 166 production fix (`allowed_kinds` null marshal) into this work. 166 is its own slot.
- **P0-A10.** Does NOT investigate or modify any of the 7 currently-passing settings ACs OR any of the other 121 specs. Out of scope.

## Skill mix (3-5)

1. **Engineer** — primary; per-AC diagnosis + narrow fix per hypothesis
2. **QATester** (optional) — for reproducing each failure under docker-compose if the engineer's local env doesn't have one ready
3. **Designer** (not needed) — no UI work
4. **Security** (not needed live) — STRIDE pass already inline

## Notes for the implementing agent

**Where slice 165's iter 2 left things:**

- 7/11 settings ACs green: AC-5, AC-6, AC-7, AC-8, AC-9, AC-10, AC-11.
- 4 remain red: AC-1, AC-2, AC-3, AC-4.
- The `authedPage` fixture rebind is in place (slice 165 iter 1).
- The `allowed_kinds = ARRAY['evidence.kind.v1']::TEXT[]` fixture workaround is in place (slice 165 iter 2).
- The production `allowed_kinds` null-marshal bug is filed as slice 166 (separate slot).

**Recommended triage order (cheap → expensive):**

1. AC-1 (text mismatch — 5 min to grep + fix)
2. AC-3 (URL pattern drift — 5-10 min)
3. AC-4 (testid or render-condition mismatch — 10-15 min)
4. AC-2 (theme picker — likely a hydration race or testid drift, 15-30 min)

If any of the 4 reveals a production bug (testid missing in source, render condition incorrect, API endpoint missing), STOP investigating that AC and file the spillover slice. Don't chase production fixes from inside the test layer.

**Trace-driven workflow:**

Each failing CI run uploads a Playwright trace.zip artifact. The trace contains:

- DOM snapshots at each step
- Network requests + responses
- Screenshots
- Action timeline

To inspect locally: download the trace, run `npx playwright show-trace path/to/trace.zip`. This is the highest-leverage diagnostic tool — most of the 4 hypotheses above resolve by ONE look at the trace's DOM snapshot at the failing step.

**Provenance.** Surfaced 2026-05-19 via maintainer request after slice 165 iter 2 landed (PR #360 at e725893) with 7/11 ACs green. The remaining 4 failures don't share the single-root-cause signature that drove slice 165's design — each needs its own narrow diagnosis.
