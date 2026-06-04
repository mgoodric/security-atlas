# 276 — Slice 254 control-detail-tabs.spec.ts e2e deep-investigation

**Cluster:** Quality / e2e
**Estimate:** 0.5-1d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** spillover from slice 275 (PR #620).

## Narrative

Slice 275 attempted to fix the racy AC-1/AC-2/AC-8/AC-9 cases in
`web/e2e/control-detail-tabs.spec.ts` by introducing a
`gotoControlDetail` helper that gates on `page.waitForResponse('coverage')`

- a 30s assertion timeout. **The fix did not resolve the failure**:
  every test still timed out at the 30s budget, with the helper hitting
  its own 30s `waitForResponse` timeout. This means the `page.route`
  mock for `/coverage` is NOT intercepting the request, even though the
  URL pattern looks correct.

Slice 275 quarantined the failing 6 tests with `.skip()` to unblock
the continuous-batch loop. This slice owns the real fix.

## Hypotheses (post-275)

1. **Bearer cookie missing in mock request**: the page's bffControlFetch may include credentials that the mock's URL pattern doesn't catch.
2. **Request URL mismatch**: the page may not actually request `/api/controls/{id}/coverage` — perhaps the BFF route is at a different path, or query params differ.
3. **Race between route registration and navigation**: even though `await page.route(...)` returns when registered, perhaps the docker-compose proxy/Next.js routing intercepts BEFORE Playwright's interceptor.
4. **`enabled: Boolean(id)` race**: maybe `useParams` is not yet resolved when `coverageQ` first fires, causing the query to be `enabled=false`, never firing, never resolving.

## Investigation strategy

1. Add `page.on('request', ...)` logging to the spec to see what URLs are actually requested.
2. Reproduce locally with `--debug` + Playwright inspector to step through.
3. Look at `web/lib/api.ts:bffControlFetch` — does it include any headers that the mock pattern needs to match?
4. Check Playwright's docs on route precedence vs Next.js dev server.

## Acceptance criteria

- [ ] AC-1: root cause documented in `docs/audit-log/276-control-detail-tabs-deep-fix-decisions.md`.
- [ ] AC-2: 5 consecutive CI runs of `control-detail-tabs.spec.ts` pass ALL 7 tests without retry (un-skip the 6 quarantined by slice 275).
- [ ] AC-3: lesson generalized in `web/e2e/README.md` if it generalizes.
- [ ] AC-4: CHANGELOG bullet under `### Fixed`.

## Anti-criteria (P0 — block merge)

- **P0-276-1**: does NOT delete the tests. They MUST be un-skipped + pass.
- **P0-276-2**: does NOT modify the production page code unless absolutely required (sibling specs on the same page DO pass; problem is isolated to this spec file).
