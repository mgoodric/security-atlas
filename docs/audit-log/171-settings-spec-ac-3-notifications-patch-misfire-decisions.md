# 171 — Settings spec AC-3 notifications PATCH misfire — decisions log

**Slice:** [171](../issues/171-settings-spec-ac-3-notifications-patch-misfire.md)
**Type:** Quality / JUDGMENT / 0.25d
**Author:** Engineer subagent
**Date:** 2026-05-19

## TL;DR

`settings.spec.ts` AC-3 (notification toggle persists server-side across reload) flipped from red → green by **swapping one verb**: `toggle.check()` → `toggle.click()` (web/e2e/settings.spec.ts:93). Net diff: +18 / −1, but only 1 substantive line of test logic changed (the other 17 lines are explanatory comments documenting the misdiagnosis chain so future AC-diagnosis sessions don't repeat it).

**Cause classification: spec drift.** The spec's locator and URL regex are correct; the production code's `onChange → patchMut.mutate` wiring is correct; the network round-trip works end-to-end. The failure is purely in Playwright's post-click state verification colliding with React's controlled-checkbox snap-back during the brief window between click and cache-invalidation. No production change, no fixture change, no backend change.

## What slice 168's engineer got wrong (so future-AC-diagnosis sessions don't repeat the chain)

Slice 168 saw AC-3 fail and hypothesized "stale fixture state from a prior CI run." They swapped `fixtures/e2e/settings.sql`'s `ON CONFLICT DO NOTHING` for `DO UPDATE SET enabled = EXCLUDED.enabled`. Their hypothesis was wrong: the fixture state was always fine.

Slice 171's filing engineer then read AC-3's continuing failure as "PATCH never fires" — the Narrative section of `docs/issues/171-…md` lists three hypotheses (H1 click-target / H2 URL regex / H3 onChange not wired to PATCH), all framed around `page.waitForResponse(/v1/me/preferences/)` 30s timeout. That framing was ALSO a misdiagnosis.

**The actual failure mode** in PR #368's CI log (run 26115121287, job 76802322769) is:

```
2) [chromium] › e2e/settings.spec.ts:71:7 › AC-3 …
   Error: locator.check: Clicking the checkbox did not change its state
   …
   - performing click action
   - click action done
   - waiting for scheduled navigations to finish
   - navigations have finished
   …
   > 93 |     await toggle.check();
```

— 756 ms (initial) and 731 ms (retry), well under any 30s timeout. Playwright's `locator.check()` clicks the checkbox AND THEN verifies `checked === true` before returning. The PATCH does fire (slice 168's engineer was wrong on that too); but React's controlled `<input type="checkbox" checked={email}>` snaps the DOM `checked` attribute back to `false` between the click and the moment `patchMut.onSuccess`'s `qc.invalidateQueries` triggers the GET refetch. Playwright's strict post-state check loses.

**Lesson for future diagnosis:** when a spec failure mentions "PATCH never fires" / "waitForResponse timeout," verify against the actual Playwright error string FIRST. Two slices in a row guessed at the failure mode from the spec body's intent rather than reading the log.

## Hypothesis match

The slice doc enumerated H1 / H2 / H3. None matched. The engineer added:

**H4 (chosen):** React-controlled-checkbox snap-back during the click→re-render→cache-invalidate window. Playwright's `locator.check()` auto-verifies post-state; `locator.click()` doesn't. The server round-trip + post-reload `toBeChecked()` assertion is the truthful invariant — `toggle.check()`'s synchronous post-state assertion is incidental to the AC's contract.

### Why H1 / H2 / H3 are rejected

| H   | Hypothesis                  | Rejection                                                                                                                                                                                                   |
| --- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| H1  | Wrong testid                | The CI log shows the locator DOES resolve: `locator resolved to <input … data-testid="settings-notif-audit_period_assignment-email"/>`. Production at `web/app/(authed)/settings/page.tsx:684` matches.     |
| H2  | URL regex mismatch          | Spec waits on `/api/me/preferences` PATCH; `web/lib/api.ts:2271` fetches exactly `/api/me/preferences` with `method: "PATCH"`. URLs match. BFF at `web/app/api/me/preferences/route.ts:24` exports `PATCH`. |
| H3  | onChange not wired to PATCH | `page.tsx:632-634` wires `onChange={(channel, next) => patchMut.mutate({...})}`, where `patchMut.mutationFn = patchMyPreferences`. Wiring is correct end-to-end.                                            |

## Why slice 168's engineer's "PATCH never fires" framing was a misdiagnosis

Slice 168's `_STATUS.md` row 168 ("AC-3 fix was a misdiagnosis (PATCH never fires; fixture upsert didn't address it)") attempted self-correction but introduced a second misdiagnosis on top of the fixture one. The PATCH DOES fire. The slice 171 doc Narrative inherited that framing without re-reading the trace, and proposed three hypotheses (H1/H2/H3) all framed around a non-existent timeout. The trace was always the source of truth — both prior diagnoses skipped it.

## Exact lines changed

| File                       | Lines | Change                                                                                                     |
| -------------------------- | ----- | ---------------------------------------------------------------------------------------------------------- |
| `web/e2e/settings.spec.ts` | 93    | `await toggle.check();` → `await toggle.click();`                                                          |
| `web/e2e/settings.spec.ts` | 73-93 | 17 lines of explanatory comment prepended documenting H4 and the misdiagnosis chain (no test-logic change) |
| `docs/issues/_STATUS.md`   | top   | New "Drift detected" block for slice 171 → in-progress + slice 171 row in the merge table later            |

No production code touched. No fixtures touched. No backend wire shapes touched.

## P0 anti-criteria audit

| ID    | Constraint                                                  | Status                                                                                      |
| ----- | ----------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| P0-A1 | Classify the fix (test-infra / spec drift / production bug) | PASS — classified as **spec drift**                                                         |
| P0-A2 | Do NOT comment out AC-3 body                                | PASS — body retained, one verb swapped                                                      |
| P0-A3 | Do NOT add new assertions                                   | PASS — assertion count unchanged (3 expects: not.toBeChecked → patchResponse → toBeChecked) |
| P0-A4 | Do NOT touch production code beyond ONE testid carve-out    | PASS — zero production lines changed                                                        |
| P0-A5 | Do NOT investigate AC-2                                     | PASS — AC-2 left as-is (slice 170 owns it)                                                  |
| P0-A6 | Do NOT modify backend wire shapes                           | PASS — no backend touched                                                                   |
| P0-A7 | Neutral test tokens only                                    | PASS — no tokens involved in the spec change                                                |
| P0-A8 | Do NOT change test harness role binding                     | PASS — fixtures untouched                                                                   |
| P0-A9 | Do NOT promote Playwright e2e to required-checks            | PASS — branch-protection unchanged                                                          |

## CodeQL / security implications

None. Zero production code changed. Test-only file change (`web/e2e/settings.spec.ts`) does not enter any compiled bundle.

## Whether AC-3 is expected to flip green

**Yes.** The truth invariant (after reload, the toggle reflects the server-persisted state) was always being verified — the failure was in the auxiliary `.check()` post-state assertion that React's controlled-checkbox lifecycle never satisfies in this window. With `.click()`:

1. Spec arms `waitForResponse(/api/me/preferences PATCH)` — already there.
2. `toggle.click()` fires the click. Playwright returns immediately; no post-state assertion.
3. React's `onChange` runs → `patchMut.mutate` posts PATCH to `/api/me/preferences`.
4. BFF proxies to backend `/v1/me/preferences`. Server writes `enabled=true`.
5. `patchResponse` resolves on the 200.
6. `page.reload()` → fresh GET returns `email: true` → component renders `checked=true`.
7. `toBeChecked()` passes.

Spillover slice for "no optimistic UI on the notifications toggle" (a real but minor UX gap — a brief snap-back is visible to users on slow networks): not filed. The spec's purpose is to assert server persistence, which it now does correctly. UX polish (optimistic checked-state during PATCH-in-flight) is out of scope for a Quality / 0.25d slice and would benefit from a dedicated design pass.

## Spillovers filed

None. This slice closes its full AC contract.

## Wall-clock

~ 25 min (well under 0.25d budget) — most of it spent downloading + reading the PR #368 trace and confirming H1/H2/H3 rejection by reading the production handler. The fix itself is one word.
