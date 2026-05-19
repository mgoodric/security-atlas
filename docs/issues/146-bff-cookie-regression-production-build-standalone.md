# 146 — Fix BFF cookie regression in production-build standalone

**Cluster:** Frontend / Quality
**Estimate:** 0.5-1d (diagnose-heavy)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 by slice 132's engineer during README-screenshot capture work (decisions log D5). The slice 132 capture pipeline ran successfully against a Next.js DEV build but FAILED against the PRODUCTION standalone build with the symptom "Could not load this panel · Unexpected token '<'... is not valid JSON" rendering on dashboard / control-detail / audit-workspace / board-pack-preview panels.

Root cause (per slice 132's diagnosis): the BFF cookie-encoded session bearer isn't recognized at the BFF → platform forwarding seam in production-build standalone mode. Browser sends the `atlas_session` cookie; BFF route hands the cookie value to the platform; platform returns HTML (login page redirect) instead of JSON; frontend JSON.parse fails with the `Unexpected token '<'` signature.

Slice 132's engineer worked around the regression by restoring the slice-057 PNGs (no breaking UI change since slice 057 → no need to re-capture for the README refresh). The underlying production-build regression remains — **every Unraid + Helm + docker-compose operator pulling main since the regression was introduced sees broken panels.**

Likely culprit: Next.js 16's standalone output mode handles cookie forwarding differently from dev mode. The slice-110 BFF cookie-forwarding helper (`web/app/api/me/sessions/_headers.ts::buildSessionsForwardHeaders`) may parse the cookie correctly in dev but lose it through Next.js's standalone-mode HTTP layer (headers/cookies pass-through semantics differ between `next dev` and `next start` against the standalone output).

**What this slice ships:**

- Diagnose the production-build standalone failure mode by running `npm run build` + `node .next/standalone/server.js` locally; reproduce the panel-failure against a known-good docker-compose backend; capture the actual cookie header path through the BFF layer.
- Fix the cookie-forwarding regression (likely a one-line change to how `buildSessionsForwardHeaders` constructs the outgoing Cookie header, or a Next.js config flag in `next.config.mjs`).
- Add a Playwright e2e spec `web/e2e/bff-cookie-production-build.spec.ts` that runs against the production-build standalone (not just dev mode) — currently NO test covers this path.
- Document the production-build-vs-dev cookie-forwarding gotcha in `docs/observability.md` or a new `docs/runbooks/` entry so future contributors don't re-introduce.

**Scope discipline (what is OUT):**

- **Refactor BFF cookie helpers across all routes** — out of scope; fix the specific regression. Broader refactor is a future slice.
- **Add production-build E2E to CI matrix** — out of scope; this slice adds ONE spec for the regression, not the broader CI infrastructure to run production-build tests on every PR.
- **Bisect when the regression was introduced** — out of scope (interesting forensic but not required for the fix). Likely culprits via git log: slice 119 (Playwright config polarity), slice 122 (api_keys idempotency), or some Next.js 16 upgrade.

## Threat model

| STRIDE                       | Threat                                                                                                                                                                                    | Mitigation                                                                                                                                                             |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a — fixing cookie forwarding doesn't change auth semantics                                                                                                                              | n/a                                                                                                                                                                    |
| **T** Tampering              | If the BFF reconstructs the Cookie header via string concatenation in the fix, header-injection vulnerabilities (CRLF) become possible. Slice 110 has the helper that validates alphabet. | Reuse slice-110's `buildSessionsForwardHeaders` validation pattern; do NOT introduce a new ad-hoc cookie-string builder.                                               |
| **R** Repudiation            | n/a                                                                                                                                                                                       | n/a                                                                                                                                                                    |
| **I** Information disclosure | A buggy fix could expose the session cookie value in a log line or error response.                                                                                                        | Sentinel-based test (slice-121 pattern): plant `test-cookie-sentinel-do-not-log-abcdef` in the fixture cookie; assert no log line or error body contains the sentinel. |
| **D** DoS                    | n/a                                                                                                                                                                                       | n/a                                                                                                                                                                    |
| **E** Elevation of privilege | n/a — fix doesn't touch authz                                                                                                                                                             | n/a                                                                                                                                                                    |

## Acceptance criteria

- [ ] **AC-1:** Reproduce the production-build regression locally — `cd web && npm run build && node .next/standalone/server.js`; hit `/dashboard` (or any panel-rendering page) authenticated; observe the failure mode. Document the exact failure shape in the decisions log.
- [ ] **AC-2:** Identify root cause via `console.log`-injection (or a more rigorous debug method) in the BFF cookie-forwarding path. Likely candidates: `web/app/api/**/route.ts` files OR `web/lib/auth.ts` cookie helpers OR `next.config.mjs`.
- [ ] **AC-3:** Ship the smallest possible fix. NO refactor of unrelated code paths. Fix lives in 1-3 files maximum.
- [ ] **AC-4:** NEW Playwright e2e `web/e2e/bff-cookie-production-build.spec.ts` that runs against the production-build standalone (separate `npm run build` step in the spec's beforeAll). Spec asserts: authenticated browser visiting `/dashboard` sees panel JSON (not HTML); cookie sentinel is forwarded correctly.
- [ ] **AC-5:** Sentinel-based info-disclosure test: planted `test-cookie-sentinel-do-not-log-abcdef` does NOT appear in any log line, error response body, or server stdout/stderr during the spec run.
- [ ] **AC-6:** Decisions log `docs/audit-log/146-bff-cookie-regression-decisions.md` records: D1 reproduction steps + observed failure shape, D2 root cause, D3 the specific change(s) made, D4 why a broader refactor wasn't required.
- [ ] **AC-7:** Add 3-paragraph entry to `docs/observability.md` or NEW `docs/runbooks/bff-cookie-forwarding.md` explaining the production-build-vs-dev cookie-forwarding gotcha + how to test for regression.
- [ ] **AC-8:** CHANGELOG entry under `[Unreleased] / Fixed`: "BFF cookie forwarding in production-build standalone (panels rendered 'Unexpected token <' since regression) — affects every Unraid / Helm / docker-compose operator (#146)".

## Constitutional invariants honored

- **#9 Manual evidence is first-class.** Operators are first-class consumers of the platform; broken panels in production builds violate this.
- **AI-assist boundary.** N/A.

## Dependencies

- **#110** BFF cookie + bearer forwarding (merged) — fix lives in this surface.
- **#082** Playwright seed-data harness (merged) — AC-4 reuses.

## Anti-criteria (P0 — block merge)

- **P0-COOKIE-1** Fix is targeted; NO unrelated refactor (scope discipline).
- **P0-COOKIE-2** Reuse slice-110's `buildSessionsForwardHeaders` validation pattern; NO new ad-hoc cookie-string builder.
- **P0-COOKIE-3** Sentinel-based info-disclosure test (AC-5) is merge-blocking — proves the fix doesn't introduce a cookie-value-in-logs leak.
- **P0-COOKIE-4** NEW production-build Playwright spec (AC-4) is merge-blocking — prevents regression from re-occurring. NO `.skip()` / `.fixme()` shortcut.
- **P0-COOKIE-5** NO vendor-prefixed test fixture tokens.

## Skill mix

- Playwright (web/e2e) — for AC-4 production-build spec.
- Next.js debugging — production-build behavior differs from dev; need understanding of `next.config.mjs` + standalone output mode.
- slice 110's `buildSessionsForwardHeaders` helper (reuse).

## Notes for the implementing agent

This is the regression slice 132's engineer surfaced in their decisions log D5 + spillover note. The slice 132 engineer documented:

> Capture pipeline actually ran but surfaced a non-slice-132 BFF cookie regression in production-build standalone — restored slice-057 PNGs (same component tree, no breaking dashboard/control-detail/audit-workspace/board-pack-preview UI change since) and filed the regression as spillover.

The slice 132 engineer's diagnostic context is the starting point. Their D5 entry has the symptom signature ("Could not load this panel · Unexpected token '<'..."). The Bisect-when-introduced is OUT of scope, but the engineer at pickup MAY want to spot-check by reverting to v1.9.0 (or earlier) and confirming the regression doesn't exist there — that narrows the introduced-in window.

**This affects every production-build operator on Unraid / Helm / docker-compose.** Triage priority: HIGH for v1.11.0 release if not already targeted.

Provenance: filed 2026-05-18 from slice 132 engineer's D5 + Spillovers section. Engineer noted "to be filed as a standalone slice by the maintainer (not in slice 132's scope)"; this is that slice.
