# Slice 399 — decisions log: re-shape bff-cookie prod-build spec + make CI leg blocking

Type: JUDGMENT (how to re-shape the two assertions so they validate the
slice-146 NODE_ENV cookie behavior under the prod-build standalone server
without green-washing the regression the spec exists to guard).

Context: slice 387 built the `frontend-playwright-prod-build` CI harness
(build:standalone → boot `node .next/standalone/web/server.js` →
`ATLAS_PROD_BUILD=1`). The harness is proven — the slice-153 logo spec passes
green against it. Its companion `bff-cookie-production-build.spec.ts` (slice 146) did NOT pass, because its body carried two dev-server-shaped assumptions
that had never executed before (the `test.skip(!ATLAS_PROD_BUILD)` guard had
never been satisfied until 387 created the harness). Slice 387 was forbidden
from touching spec bodies and filed those fixes here. This slice fixes the two
assertions, re-includes the spec, and flips the leg to blocking.

---

## D1 — Assertion 1: RSC-aware regression guard, not a client-BFF-call count

**Problem.** The original assertion was:

```ts
expect(bffResponses.length).toBeGreaterThan(0); // browser-observed /api/dashboard/ responses
for (const r of bffResponses)
  expect.soft(r.contentType).toContain("application/json");
```

Under the **dev server** the dashboard panels are client components whose
TanStack queries fire browser-side `/api/dashboard/*` fetches, so
`bffResponses.length > 0` held. Under the **production build** the panels are
server-rendered (RSC fetches the data server-side during SSR), so the browser
fires **zero** client-side `/api/dashboard/` calls. `length > 0` receives 0 and
fails — a false assertion for the prod build, not a product regression (387's
failure-run snapshot shows a fully-authenticated dashboard).

**What the slice-146 regression actually is.** A `NODE_ENV`-coupled cookie
attribute (the blunt `secure: process.env.NODE_ENV === "production"`) drops the
`atlas_jwt` cookie on the BFF round-trip under the standalone server's plain
HTTP transport. The proxy then sees no bearer, redirects to `/login`, and the
panel data resolves to **login HTML instead of JSON**. The browser-visible
signature is `JSON.parse("<!DOCTYPE …")` throwing **"Unexpected token '<'"**,
which the panel renders in its error Alert ("Could not load this panel ·
Unexpected token '<'", `web/components/dashboard/panel-card.tsx:70`).

**Re-shape (three layers, all RSC-valid, none green-washed):**

1. **Authenticated-render positive.** Assert the topbar "Sign out" control is
   visible (`web/components/shell/topbar.tsx:142`). If the cookie had dropped,
   the standalone server's RSC fetch would have failed auth and rendered the
   login page — no "Sign out". This is the load-bearing assertion: it proves
   the cookie survived the standalone-transport round-trip, which is exactly
   the property slice 146 fixed.

2. **Regression-signature negative.** Assert the JSON-parse-HTML signature
   ("Unexpected token '<'") does **not** appear anywhere on the rendered page,
   and that no dashboard panel shows its error Alert
   (`[data-testid$="-error"]` under the panel grid). This is the precise
   manifestation of the slice-146 regression in any panel that _does_ fetch
   client-side. It is scoped to the regression signature, NOT a blanket
   "no panel ever errors" — a thin CI seed legitimately producing a non-HTML
   panel error would not carry the "Unexpected token '<'" string, so the guard
   does not false-fail on unrelated panel errors.

3. **Content-type guard, conditional.** Keep capturing `/api/dashboard/`
   responses and assert each observed one is `application/json` — but DROP the
   `length > 0` requirement. When the prod build fires zero client-side calls
   the loop is a clean no-op; when a surface _does_ drive a client-side fetch
   (now or in future), the original JSON-not-HTML content-type guard still
   executes. This preserves the spec's stated intent ("BFF returns JSON, not
   the login HTML") wherever a browser-side BFF call exists, without asserting
   a call that the prod build's RSC rendering does not make.

**Why this is not green-washing.** If the slice-146 fix is reverted, the
standalone server drops the cookie, the RSC dashboard render fails auth, the
page becomes the login page → "Sign out" disappears (layer 1 fails) and/or any
client panel fetch surfaces "Unexpected token '<'" (layer 2 fails). The spec
still fails red on the exact regression it guards. We removed only the
dev-server-shaped _mechanism_ assumption (that the guard is observed via a
client-side fetch count), not the _property_ (cookie survives → JSON not HTML →
authenticated render).

## D2 — Assertion 2: cookie domain from baseURL, not authedPage.url()

The original built the cookie domain from `new URL(authedPage.url())` BEFORE
any navigation, so `authedPage.url()` was `about:blank`, the parsed hostname
was empty, and Playwright rejected the cookie
(`Cookie should have a url or a domain/path pair`).

Fix: derive the domain from the Playwright `baseURL` fixture param (the served
origin) — the identical pattern `web/e2e/fixtures.ts:85` already uses to inject
the bearer cookie. This is origin-correct (the cookie is set against the real
baseURL host, e.g. `localhost`), navigation-order-independent, and consistent
with the existing fixture. `secure` is derived from the baseURL protocol so the
cookie is valid under both http (standalone CI) and https.

## D3 — ATLAS_JWT_COOKIE, not SESSION_COOKIE

Slice 397 renamed `SESSION_COOKIE` → `ATLAS_JWT_COOKIE` (`web/lib/auth.ts:20`).
The spec already imported `ATLAS_JWT_COOKIE`; both assertions use it. No
SESSION_COOKIE reference remains.

## D4 — test.skip(!ATLAS_PROD_BUILD) guard retained

The guard is the mechanism that scopes the spec to the standalone leg (387 D3).
It is retained unchanged. Only the spec bodies and the stale "held to slice
399" quarantine comments changed.

## D5 — CI: re-include the spec + drop continue-on-error (blocking)

In `.github/workflows/ci.yml`, the `frontend-playwright-prod-build` job's
Playwright invocation now names BOTH specs
(`bff-cookie-production-build.spec.ts logo-render-production-build.spec.ts`),
and `continue-on-error: true` is removed so the leg blocks on red. The job-level
and step-level comments are updated to drop the "held to slice 399 / advisory"
language and state that the leg is now blocking on both prod-build specs.
`.github/branch-protection.json` is NOT modified (per 387 D6, promotion to a
required check is a separate operational step after green runs).

## D6 — Determinism (no fixed sleeps)

Both assertions use Playwright auto-waiting: `goto(..., { waitUntil:
"networkidle" })` plus `expect(locator).toBeVisible()` / `toHaveCount(0)`
web-first assertions that retry until the condition holds or times out. No
`page.waitForTimeout`. Response capture uses the `response` event listener
(no polling).

## D7 — Verification posture

Full local standalone boot requires the whole backend (Postgres, NATS, MinIO,
atlas, runtime JWT mint), which this worktree cannot stand up cheaply. The
authoritative verification is the `frontend-playwright-prod-build` CI leg, which
now runs both specs against a real standalone server. Locally we verify:
TypeScript compiles, prettier/actionlint clean, and the spec parses under
`playwright test --list`. Documented honestly rather than claiming a local green
that was not produced.
