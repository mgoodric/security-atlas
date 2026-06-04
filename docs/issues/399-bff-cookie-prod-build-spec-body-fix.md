# 399 — Re-shape bff-cookie-production-build.spec.ts for the prod-build standalone server

**Cluster:** Quality / e2e
**Estimate:** 0.5-1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 387, captured per continuous-batch policy.

Slice 387 added the CI harness that builds + boots the Next.js
`output: "standalone"` server and runs the two production-build
Playwright specs against it with `ATLAS_PROD_BUILD=1`. The slice-153
`logo-render-production-build.spec.ts` passes green against that server
(the harness is proven). Its companion
`web/e2e/bff-cookie-production-build.spec.ts` (slice 146) does NOT pass —
not because of a product regression, but because the spec body carries
two **dev-server-shaped assumptions that had never executed before**
(the `test.skip(!ATLAS_PROD_BUILD, …)` guard had never been satisfied
until slice 387 created the harness). Both surfaced on the spec's first
real run:

1. **`dashboard panel BFF returns JSON, not the login HTML`** asserts
   `expect(bffResponses.length).toBeGreaterThan(0)` where `bffResponses`
   counts browser-observed `/api/dashboard/` responses. Under the
   **production build** the dashboard panels are server-rendered (React
   Server Components fetch the data server-side during SSR), so the
   browser fires **zero** client-side `/api/dashboard/` calls — the
   assertion receives 0 and fails. The page renders **fully
   authenticated** (the failure-run page snapshot shows the "Sign out"
   button, the "API key" identity, the full nav, and the "Program"
   heading), so auth carries through fine — this is NOT the slice-146
   `NODE_ENV`-coupled cookie regression (that would have rendered a login
   page / returned login HTML). The assertion needs re-shaping to the
   prod-build reality: assert that the panel **content resolved** (no
   "Could not load this panel · Unexpected token '<'" error surface)
   rather than that a client-side BFF call occurred — OR drive a surface
   that genuinely does a browser-side BFF fetch under the prod build (an
   interaction that triggers a TanStack Query refetch), so the regression
   the spec means to guard (JSON-not-HTML from the BFF) is actually
   exercised.

2. **`session cookie sentinel never appears in browser-observable
surfaces`** calls
   `context.addCookies([{ … domain: new URL(authedPage.url() || "http://localhost:3000").hostname … }])`
   **before any navigation**, so `authedPage.url()` is `about:blank`,
   the parsed `hostname` is empty, and Playwright rejects the cookie with
   `browserContext.addCookies: Cookie should have a url or a domain/path
pair`. Fix: navigate first (or use the `baseURL` / a `url:` field
   instead of a `domain`/`path` pair), so the cookie has a valid domain.

## What

Re-shape the two test bodies in
`web/e2e/bff-cookie-production-build.spec.ts` so they pass against the
production-build standalone server while still asserting the slice-146
regression (BFF returns JSON, not login HTML) AND the sentinel-non-leak
property. Then in `.github/workflows/ci.yml`:

- add `bff-cookie-production-build.spec.ts` back to the
  `frontend-playwright-prod-build` job's Playwright invocation
  (alongside the logo spec), and
- drop `continue-on-error: true` from that job so the leg becomes
  blocking once both specs are green.

## Scope discipline

- This is the spec-BODY change slice 387 explicitly deferred (slice 387
  was forbidden from touching the spec bodies).
- DOES NOT change the harness wiring slice 387 built (build:standalone +
  standalone boot + ATLAS_PROD_BUILD gating) — only the spec assertions +
  the two ci.yml lines that re-include the spec and flip the job to
  blocking.

## Acceptance criteria

- [ ] AC-1: `dashboard panel BFF returns JSON` re-shaped to a
      prod-build-valid assertion that still guards the slice-146
      JSON-not-HTML regression; passes against the standalone server.
- [ ] AC-2: `session cookie sentinel …` fixed so `addCookies` receives a
      valid domain/url; passes against the standalone server.
- [ ] AC-3: `frontend-playwright-prod-build` runs BOTH prod-build specs
      and `continue-on-error` is removed (the leg blocks on red).
- [ ] AC-4: the slice-153 logo spec still passes (no regression to the
      working half).

## Dependencies

- #387 — the standalone CI harness this builds on. Must merge first.
- #146 — the spec being re-shaped.

## Cross-references

- Slice 387 decisions log
  (`docs/audit-log/387-e2e-prod-build-ci-harness-decisions.md`).
- Slice 351 coverage matrix flows #10, #11.
