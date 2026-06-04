# 171 — Settings spec AC-3 notifications PATCH never fires (slice 168 misdiagnosis follow-on)

**Cluster:** Quality
**Estimate:** 0.25d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** Surfaced during slice 168 (diagnose + fix remaining 4 settings.spec.ts AC failures), captured as follow-up per continuous-batch policy.

Slice 168's engineer diagnosed AC-3 (notification toggle persists server-side across reload) as a **test-infra gap**: the fixture `ON CONFLICT DO NOTHING` left a stale `user_notification_preferences` row untouched. Their fix swapped the fixture's upsert to `ON CONFLICT (...) DO UPDATE SET enabled = EXCLUDED.enabled`.

CI Playwright on PR #368 post-fix confirmed: AC-3 still red. The fixture change didn't address the actual failure mode.

The failure trace shows: `page.waitForResponse(/v1/me/preferences)` 30s timeout. The PATCH request **never fires** at all. That's not a fixture problem (which would surface as a wrong-initial-state, not a missing request). It's a click-target or interaction problem.

**WHAT.** Engineer reproduces locally + reads the trace artifact to identify why the toggle click doesn't trigger a PATCH. Three likely causes (cost-ordered):

- **H1 (cheapest):** spec's toggle locator (line ~71-90 of `web/e2e/settings.spec.ts`) matches the wrong element. Maybe a non-clickable label, or a disabled toggle, or a hidden state. Grep the actual `NotificationsCard` testid scheme + compare against spec.
- **H2:** the toggle IS clicked but the PATCH URL doesn't match the spec's regex `/v1/me/preferences/`. Maybe the URL is `/v1/me/notifications` or `/api/me/preferences/email`. Inspect the trace's network tab for ANY request that fires post-click.
- **H3:** the toggle's `onClick` is wired to a different handler that doesn't PATCH (e.g., optimistic UI update with no roundtrip, or PATCH is gated on form-submit not toggle-change). Engineer reads `web/app/(authed)/settings/page.tsx` Notifications Card section to confirm.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT investigate AC-2 (theme picker hydration — slice 170 owns).
- Does NOT touch the production toggle wiring (P0-A3 carry-over from slice 168) unless the cause is a 1-line testid drift that's clearly correctable; production changes get filed as further spillover.
- Does NOT add new assertions.
- Does NOT investigate the 9 currently-passing settings ACs.

## Threat model

Same as slice 168 (test fixture + spec modifications). STRIDE pass inherited: has-mitigations.

**Anti-criterion (P0-A1 carryover):** Engineer classifies the fix as test-infra gap / spec drift / production bug.

## Acceptance criteria

- **AC-1.** Engineer identifies the cause (H1/H2/H3 or new hypothesis) + records in `docs/audit-log/171-settings-spec-ac-3-notifications-patch-misfire-decisions.md`.
- **AC-2.** Apply narrow fix (≤ 5 lines) in spec OR fixture OR production-source (with 1-line testid carve-out only).
- **AC-3.** Settings spec AC-3 transitions red → green in CI Playwright. Net: 9/11 → 10/11 settings ACs green.
- **AC-4.** No regression in the 9 currently-passing settings ACs.
- **AC-5.** No regression in the other 121 specs.
- **AC-6.** Decisions log captures (a) cause classification (b) hypothesis match (c) what slice 168's engineer got wrong (the fixture upsert assumption) so future-AC-diagnosis sessions don't repeat the misdiagnosis.

## Constitutional invariants honored

- Slice 168's contract: 9 of 11 ACs delivered; this slice closes the 10th (AC-3).
- AC-2 is owned by slice 170 (theme picker hydration); orthogonal to this work.
- Test discipline: no commented-out bodies; per-AC narrow fixes.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — settings page operator surface.
- `Plans/canvas/09-tech-stack.md` — Playwright e2e discipline.

## Dependencies

- **#168** (slice 168 follow-on) — `merged` at 9f70f08. This slice completes 168's AC-3 contract.
- **#170** (theme picker hydration) — `ready`, parallel.

## Anti-criteria (P0 — block merge)

- **P0-A1.** Engineer MUST classify the fix (test-infra / spec drift / production bug).
- **P0-A2.** Does NOT comment out AC-3 body.
- **P0-A3.** Does NOT add new assertions.
- **P0-A4.** Does NOT touch production code beyond ONE testid attribute addition (if documented in decisions log).
- **P0-A5.** Does NOT investigate AC-2 (slice 170 owns).
- **P0-A6.** Does NOT modify backend wire shapes.
- **P0-A7.** No real-data UUIDs.
- **P0-A8.** No test harness role binding changes.
- **P0-A9.** Does NOT promote `Frontend · Playwright e2e` to required-checks.

## Skill mix (3-5)

1. **Engineer** — primary; trace-driven diagnosis + narrow fix.
2. **QATester** (optional) — for local docker-compose repro.

## Notes for the implementing agent

**Where slice 168 left things:**

- AC-3 spec still asserts the same toggle interaction (line ~71-90 of `web/e2e/settings.spec.ts`).
- Fixture upsert (`ON CONFLICT DO UPDATE SET enabled = EXCLUDED.enabled`) IS in place at `fixtures/e2e/settings.sql` — leave it.
- The slice 168 engineer's misdiagnosis was assuming the failure was about "stale fixture state". It's actually about "PATCH never fires post-click."

**Recommended workflow:**

1. Download the failing CI trace artifact from PR #368's latest run.
2. Open trace in `npx playwright show-trace`.
3. Click on the AC-3 failure timeline.
4. Inspect the network tab — does ANY request fire after the toggle click? If yes, what URL? If no, the click target is wrong.
5. Apply the narrowest fix that addresses the actual cause.

**Provenance.** Surfaced 2026-05-19 by slice 168 PR #368 CI evidence — AC-3 misdiagnosed by engineer; fixture upsert change didn't resolve the actual failure mode (PATCH never fires).
