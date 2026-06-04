# 153 — Logo not rendering in header + login screen on production-build standalone (v1.10.0)

**Cluster:** Frontend
**Estimate:** 0.5d (diagnose-heavy)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced 2026-05-18 from operator report on v1.10.0 Unraid deployment:

> "The logo is still not showing on the header or the login screen"

Slice 123 (merged 2026-05-18, commit `97e3eb4`) fixed the v1.9.0 logo issue by adding a `PUBLIC_STATIC_FILES` Set to `web/proxy.ts` that exempts the 7 unauthenticated-referenced assets (`logo-light.svg`, `logo-dark.svg`, `og-image.png`, `twitter-card.png`, app icons) from the auth check.

The fix worked in dev mode (Playwright spec passed). On v1.10.0 production-build standalone (Unraid deployment), the logo STILL doesn't render. Two possible root causes:

1. **Same family as slice 146 (BFF cookie regression).** Next.js 16's standalone output mode handles middleware differently from dev. The `web/proxy.ts` matcher may not apply the `PUBLIC_STATIC_FILES` exemption in standalone — the exempt-list lives in middleware that may not run for static-asset routes in production.
2. **Asset path mismatch.** Header component references `/logo-light.svg` but standalone build may serve assets from a different path (`_next/static/...` or CDN-relative).

**What this slice ships:**

- Reproduce locally: `cd web && npm run build && node .next/standalone/server.js` then load `/login` + `/dashboard`; observe console + network for logo requests.
- Identify whether `proxy.ts` middleware runs for `/logo-*.svg` in standalone mode (likely doesn't; Next.js standalone routes static files outside middleware).
- Fix: move the logo SVGs under `web/public/_next/static/...` or `web/public/static-public/` so they're served by Next.js's static file handler (bypasses middleware entirely).
- Alternative fix: explicitly mark the asset paths in `next.config.mjs` `rewrites` / `headers` to exempt from middleware.
- Add a Playwright e2e spec that runs against the production-build standalone (slice 146's pattern) asserting logo SVG returns 200 + `Content-Type: image/svg+xml` (not HTML).

## Acceptance criteria

- [ ] AC-1: Reproduce locally via `npm run build` + `node .next/standalone/server.js`; capture network request for `/logo-light.svg` (status + content-type).
- [ ] AC-2: Root cause identified + documented in decisions log (D1: middleware-doesn't-run-for-static vs path-mismatch).
- [ ] AC-3: Fix lands in 1-3 files (`web/proxy.ts`, `next.config.mjs`, or asset re-organization).
- [ ] AC-4: Playwright e2e `web/e2e/logo-render-production-build.spec.ts` against production-build standalone; asserts both `/logo-light.svg` + `/logo-dark.svg` return 200 + correct content-type.
- [ ] AC-5: Login + dashboard pages both render the logo (visual confirmation).
- [ ] AC-6: CHANGELOG entry: "Logo SVGs render correctly in production-build standalone (#153)".

## Threat model

| STRIDE                       | Threat                                                                                                                                                | Mitigation                                                                                                                       |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| **I** Information disclosure | A fix that moves logo SVGs to a public CDN path may inadvertently expose OTHER files in the same directory. Path-traversal-via-misconfig is the risk. | Restrict the public-asset path to a NAMED set of files (whitelist). Never `app.use('/public-static', static('./public'))`-style. |

## Dependencies

- **#123** Logo render fix (merged 2026-05-18) — worked in dev, didn't work in standalone.
- **#146** BFF cookie regression in production-build standalone (`ready`, in flight as PR #304) — likely same family of bugs; share diagnostic approach.

## Anti-criteria (P0 — block merge)

- **P0-LOGO-1** Fix MUST work in production-build standalone (not just dev mode).
- **P0-LOGO-2** Playwright e2e against production-build standalone is merge-blocking (parallels slice 146's pattern).
- **P0-LOGO-3** NO scope creep into other logo / branding / design changes.
- **P0-LOGO-4** NO vendor-prefixed test fixture tokens.

## Notes for the implementing agent

Operator hit this on v1.10.0 v1.9.0 → both broken. Slice 123 was the dev-mode fix; this is the production-build fix. Likely small surface (1-3 files).

Closely related to slice 146 (BFF cookie). The two share the "Next.js standalone behavior differs from dev" root cause family.

Provenance: filed 2026-05-18 from operator v1.10.0 report.
