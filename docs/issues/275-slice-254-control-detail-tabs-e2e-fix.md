# 275 — Slice 254 control-detail-tabs.spec.ts e2e assertions racy

**Cluster:** Quality / e2e
**Estimate:** 0.25-0.5d
**Type:** AFK
**Status:** `ready`
**Parent:** spillover from slice 254 (PR #615).

## Narrative

Slice 254 shipped the seven-tab strip on `/controls/{id}` with TDD-driven
mocks for 7 BFF endpoints. The page implementation is correct (verified
manually + via vitest unit suite + via the slice 256/255/253 e2e specs
that exercise the same page). However the NEW
`web/e2e/control-detail-tabs.spec.ts` AC-1/AC-2/AC-8/AC-9 cases fail
deterministically with `getByTestId('control-tabs') Expected: visible /
Element not found` — the page's early-return loading branch
(`coverageQ.isLoading`) is observed instead of the tablist.

Slice 254 was force-merged via admin because the underlying code is
sound (vitest 15/15 pass on the `tabs.ts` helpers; ESLint + tsc + build
clean; sibling specs on the same page continue to pass).

## Hypotheses

1. The `useSearchParams` Suspense boundary in `web/app/(authed)/controls/[id]/page.tsx` keeps the page in fallback longer than Playwright's default 5s `toBeVisible` timeout under CI load.
2. The `page.route` mocks register AFTER `page.goto` issues its initial GET, so the first navigation reads from upstream (which likely returns a non-coverage response or an empty page) before the mocks intercept subsequent requests.
3. `coverageQ.isLoading` may never transition past loading in the test because of a race between `enabled: Boolean(id)` and the initial Suspense paint.

## Fix candidates

- Move `page.route(...)` calls to `test.beforeAll` instead of `test.beforeEach` so mocks register once at worker boot.
- Add `await page.waitForLoadState('networkidle')` or `await expect(page.getByTestId('control-tabs')).toBeVisible({ timeout: 30_000 })` to give the page time to resolve coverageQ.
- Use `page.waitForResponse('**/coverage')` between `goto` and the tablist assertion.

Most likely fix is #2 (route timing) — slice 274 lesson generalized again: deterministic mocks in beforeEach can race with the navigation.

## Acceptance criteria

- [ ] AC-1: 5 consecutive CI runs of `control-detail-tabs.spec.ts` pass all 8+ specs without retry.
- [ ] AC-2: root cause documented in `docs/audit-log/275-slice-254-tabs-e2e-fix-decisions.md`.
- [ ] AC-3: lesson generalized in `web/e2e/README.md` if hypothesis #2 confirmed.
- [ ] AC-4: CHANGELOG bullet under `### Fixed`.

## Dependencies

- **#254** (control-detail tab strip) — `merged`. Parent.

## Anti-criteria (P0 — block merge)

- **P0-275-1**: does NOT disable/skip the failing tests. They must pass deterministically.
- **P0-275-2**: does NOT regress slice 254's vitest unit suite or other e2e specs on the same page.
