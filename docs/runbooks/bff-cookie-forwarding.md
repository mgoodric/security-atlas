# Runbook — BFF cookie forwarding in production-build deployments

## TL;DR

If the dashboard / control-detail / audit-workspace / board-pack-preview panels render `Could not load this panel · Unexpected token '<'... is not valid JSON` against a production deployment, the most likely cause is that the session cookie has the `Secure` attribute set but the browser is reaching the deployment over plain HTTP. The fix in slice 146 picks `Secure` per-request from the `X-Forwarded-Proto` / `Forwarded` headers; this runbook explains how to verify, and what to do when the symptom recurs on a related surface.

## Symptom

Six (or more) of the dashboard panels render the same error string at once:

```
Could not load this panel
Unexpected token '<', "<!DOCTYPE "... is not valid JSON
```

The Network tab shows BFF requests like `GET /api/dashboard/drift` returning HTTP 307 followed by `GET /login` returning HTTP 200 with `Content-Type: text/html`. The frontend code (`web/lib/api.ts::bffControlFetch`) calls `await res.json()` on the HTML and JSON.parse throws the unexpected-token error.

The same surface works against `npm run dev` (the Next.js dev server) but fails against `node .next/standalone/server.js` (the production-build standalone output that the shipped `web` Docker image runs).

## Root cause (slice 146)

The `signIn` server action at `web/app/login/actions.ts` calls `cookies().set(SESSION_COOKIE, ...)` with the `secure` attribute set based on `process.env.NODE_ENV`. Before slice 146 the check was:

```ts
secure: process.env.NODE_ENV === "production",
```

In dev (`npm run dev`) `NODE_ENV` is `development` so `secure` is `false`, and the browser sends the cookie over plain HTTP localhost.

In a production-build standalone deployment (`node .next/standalone/server.js`, which is what the published `web` Docker image runs) `NODE_ENV` is `production` so `secure` is `true`. Browsers refuse to send `Secure` cookies over plain HTTP. The cookie therefore never reaches the BFF for self-hosted operators serving over plain HTTP (Unraid without TLS, docker-compose without a reverse proxy, local production-build smoke runs).

`web/proxy.ts` then sees `request.cookies.get(SESSION_COOKIE)` as `undefined` for every non-exempt path (including all `/api/**` BFF routes) and redirects to `/login`. The browser `fetch` follows the redirect, gets the login HTML, and the BFF wrapper's `await res.json()` blows up.

After slice 146 the `secure` attribute is picked per-request:

```ts
secure: shouldUseSecureCookie(reqHeaders),
```

where `shouldUseSecureCookie` (at `web/lib/secure-cookie.ts`) checks `X-Forwarded-Proto` (the de-facto-standard reverse-proxy header) first, then the RFC 7239 `Forwarded` header, then defaults to `false` (so the cookie round-trips on plain-HTTP self-host deployments).

## How to verify a deployment is healthy

1. Sign in to the deployment.
2. Inspect the `sa_session_token` cookie in the browser dev tools' Application > Cookies pane. The `Secure` column should read:
   - `false` for a plain-HTTP deployment (Unraid + no TLS, docker-compose default)
   - `true` for an HTTPS deployment (Helm with cert-manager, NPM with a Let's Encrypt cert, etc.)
3. Visit `/dashboard`. Every panel should render data (or its honest empty / loading state) — none should render `Unexpected token '<'`.
4. In the browser Network tab, filter on `/api/dashboard/`. Every BFF response should have `Content-Type: application/json`. If any show `Content-Type: text/html` the cookie is not making it back to the server.

## How to test for regression (developer)

Run the unit test (always-on, fast):

```sh
cd web
npm run test -- --run secure-cookie
```

For an end-to-end regression check, run the quarantined Playwright spec against a real production-build standalone server:

```sh
cd web
npm run build
node .next/standalone/server.js &
ATLAS_PROD_BUILD=1 TEST_BEARER=<your bearer> \
  npx playwright test bff-cookie-production-build.spec.ts
```

The spec asserts BFF responses are JSON and the planted cookie sentinel does not leak into any browser-observable surface.

## Future-proofing

Two patterns to avoid when adding new cookies or new BFF routes:

1. Do NOT couple any cookie-attribute decision to `process.env.NODE_ENV` — pick per-request via `shouldUseSecureCookie` or an equivalent transport-aware helper.
2. Do NOT broaden `web/proxy.ts` matcher exemptions to "fix" a BFF auth issue. The proxy.ts redirect is correct behavior for an unauthenticated browser; the bug is in how the cookie was set, not in how the proxy reads it.

If the symptom recurs against a new BFF route, the first thing to check is what the Network tab shows for the failing BFF request — a `307 → /login` redirect points at this runbook; anything else (e.g. a real 500) is a different bug.
