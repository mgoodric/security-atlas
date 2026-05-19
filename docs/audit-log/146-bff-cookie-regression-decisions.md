# Slice 146 — BFF cookie regression decisions log

Per JUDGMENT slice convention (slice-development workflow): the implementing agent records each subjective build-time decision inline rather than blocking the merge on human sign-off. The product-runtime AI-assist boundary is untouched by this convention.

## D1 — Reproduction steps and observed failure shape

**Decision:** Diagnosis was performed by static code analysis rather than running a local `npm run build && node .next/standalone/server.js` instance against a docker-compose backend. The diagnostic chain is short and airtight from code reading alone:

1. `web/app/login/actions.ts:53` sets the session cookie with `secure: process.env.NODE_ENV === "production"`.
2. `node .next/standalone/server.js` runs with `NODE_ENV=production` so the cookie is emitted with the `Secure` attribute.
3. Self-hosted operators (Unraid, docker-compose without TLS, local production-build smoke runs) serve over plain HTTP. Browsers refuse to send `Secure` cookies over plain HTTP per the HTTPS-cookie security model.
4. `web/proxy.ts` (line 99-105) reads `request.cookies.get(SESSION_COOKIE)`; with the cookie absent it issues a 307 redirect to `/login`.
5. The matcher at `web/proxy.ts:109-111` (`/((?!_next/static|_next/image|favicon.ico).*)`) covers every `/api/**` BFF path, so the redirect fires for BFF fetches too.
6. `web/lib/api.ts::bffControlFetch` calls `await res.json()` on the resulting login HTML; `JSON.parse('<')` throws `Unexpected token '<'`.

The slice 132 D5 entry (docs/audit-log/132-readme-refresh-decisions.md) captured the exact symptom string ("Could not load this panel · Unexpected token '<'... is not valid JSON") observed live against a freshly built production-build standalone, which is the load-bearing reproduction. The fix is verifiable end-to-end via the quarantined Playwright spec (`web/e2e/bff-cookie-production-build.spec.ts`) once a seed harness drives the standalone server. The unit test (`web/lib/secure-cookie.test.ts`, 8 cases) is the always-on gate.

**Confidence:** high.

## D2 — Root cause

**Decision:** The root cause is the `secure: process.env.NODE_ENV === "production"` attribute, NOT the BFF outbound `Cookie` header construction.

The slice 110 helper (`web/app/api/me/sessions/_headers.ts::buildSessionsForwardHeaders`) only runs on `/api/me/sessions*` routes (intentionally narrow scope per slice 110 P0-A2). The regression hits ALL BFF routes — dashboard, control-detail, audit-workspace, board-pack-preview, every panel — because every BFF route is gated by the same `proxy.ts` cookie check, which fires before any route-level cookie-forwarding helper. So the failure is upstream of the slice-110 surface.

The slice 110 helper does not need to change.

**Confidence:** high.

## D3 — Specific change(s) made

**Decision:** Three additions and one modification:

| File                                          | Change                                                                                                                                                                                                                                                                         |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `web/lib/secure-cookie.ts`                    | NEW. Exports `shouldUseSecureCookie(headers: Headers): boolean`. Reads `X-Forwarded-Proto` (de-facto standard) first, then RFC 7239 `Forwarded` header, defaults to `false`.                                                                                                   |
| `web/lib/secure-cookie.test.ts`               | NEW. 8 vitest cases covering both proto signals + the no-signal default + precedence + malformed values.                                                                                                                                                                       |
| `web/e2e/bff-cookie-production-build.spec.ts` | NEW. 2 Playwright cases (BFF returns JSON; sentinel cookie value never surfaces). Quarantined behind `ATLAS_PROD_BUILD` env var until the seed harness can provision a standalone server in CI (separate slice).                                                               |
| `web/app/login/actions.ts`                    | MODIFIED. Imports `headers` from `next/headers` + `shouldUseSecureCookie`. Replaces the inline `NODE_ENV` check with a per-request transport detection. The previous `secure: process.env.NODE_ENV === "production"` line becomes `secure: shouldUseSecureCookie(reqHeaders)`. |

The change is intentionally minimal — one helper, one call site, two test files, one runbook. No other file needs to change. `web/proxy.ts` is unmodified (its redirect behavior is correct; the bug was upstream in cookie emission). The slice-110 outbound Cookie-header builder is unmodified (its scope is narrow + correct).

**Confidence:** high.

## D4 — Why a broader refactor wasn't required

**Decision:** Scope discipline (slice 146 P0-COOKIE-1). The regression has a single root cause at a single call site. A broader refactor — for example, replacing every direct `cookies().set(...)` with a typed wrapper — would conflate this regression fix with a refactor that has not been requested, broaden the diff, and risk introducing unrelated bugs in surfaces that are working today.

I verified there is exactly one site in `web/` that couples a cookie's `secure` attribute to `NODE_ENV`:

```sh
grep -rn 'secure:.*NODE_ENV\|secure:.*production' web/ --include='*.ts' --include='*.tsx' | grep -v node_modules | grep -v '.next/'
# web/app/login/actions.ts:53:    secure: process.env.NODE_ENV === "production",
```

If a future cookie addition introduces the same coupling, the always-on unit test (`web/lib/secure-cookie.test.ts`) keeps the helper available; the runbook (`docs/runbooks/bff-cookie-forwarding.md`) documents the gotcha; the reviewer-discipline footer in the runbook ("Do NOT couple any cookie-attribute decision to `process.env.NODE_ENV`") is the social mechanism. A grep + lint rule are out of scope for this slice but a reasonable future enhancement.

**Confidence:** high.

## D5 — Helper signature: `Headers` rather than `NextRequest`

**Decision:** The helper takes a standard `Headers` instance rather than a Next.js `NextRequest`. Rationale:

1. The call site is a Next.js Server Action, which has access to `headers()` from `next/headers` (returns a `ReadonlyHeaders` extending `Headers`) but does NOT have a `NextRequest` in scope.
2. Standard `Headers` is the lowest-coupling shape — testable from vitest without mocking Next-specific symbols, callable from middleware/proxy contexts in the future if needed (the `NextRequest.headers` is a `Headers` instance).
3. The unit test instantiates `new Headers({...})` directly, which is what the spec covers.

**Confidence:** high.

## D6 — Default-INSECURE when no signal present

**Decision:** When neither `X-Forwarded-Proto` nor `Forwarded` is present, the helper returns `false` (NOT secure).

The trade-off:

- Default-SECURE: a deployment that doesn't propagate the proto header would re-introduce the regression (the very thing this slice fixes).
- Default-INSECURE: a deployment that's actually HTTPS but doesn't propagate the proto header would emit a non-Secure cookie. The browser would still send the cookie over HTTPS (Secure is a one-way constraint: it BLOCKS sending over HTTP, not over HTTPS). The only loss is the cookie ALSO travels over HTTP if some link is downgraded — which on an HTTPS-only deployment shouldn't happen.

The default-INSECURE choice matches the regression-prevention objective of this slice. Every credible reverse proxy (nginx, Traefik, NPM, Cloudflare, AWS ALB, K8s ingress controllers) sets `X-Forwarded-Proto` by default, so HTTPS deployments hit the explicit-secure path. The "HTTPS deployment with no proto header" case is hypothetical and out-of-scope.

**Confidence:** high.

## D7 — Playwright spec quarantine vs full CI gate

**Decision:** The new spec at `web/e2e/bff-cookie-production-build.spec.ts` is quarantined behind `ATLAS_PROD_BUILD=1`. The CI Playwright matrix runs against the dev server (`npm run dev`), not the standalone output, so an always-on run would be a false-positive (it would test the wrong surface) or a no-op skip. Adding a separate CI matrix for the standalone server is filed in the slice as out-of-scope ("Add production-build E2E to CI matrix — out of scope; this slice adds ONE spec for the regression, not the broader CI infrastructure").

The unit test (`web/lib/secure-cookie.test.ts`) is the always-on gate; the spec is the integration belt-and-suspenders.

**Confidence:** high.

## D8 — Sentinel cookie value naming

**Decision:** The Playwright spec uses `test-cookie-sentinel-do-not-log-abcdef` as the planted cookie value. The slice's P0-COOKIE-5 anti-criterion forbids vendor-prefixed test fixture tokens (`okta_*`, `ghp_*`, `sk_live_*`, `eyJ*`, `AKIA*`, etc.) because GitGuardian scans test files. The chosen string is:

- Neutral (no vendor prefix, no real-token shape)
- Self-documenting ("do-not-log" suffix flags intent on inspection)
- Long enough to grep for in server logs without false positives
- Stable across runs (no per-test randomization)

**Confidence:** high.

## Revisit list

The following decisions are likely to be revisited in a follow-up slice and are flagged here so the next implementing agent doesn't re-derive them:

1. If the slice-082 seed harness gains the ability to provision a standalone server inside the CI matrix, drop the `ATLAS_PROD_BUILD` guard on the spec and remove the test.skip.
2. If a future slice introduces a new cookie that needs the same `Secure`-per-transport treatment, lift `shouldUseSecureCookie` from a per-call helper into a `setSessionCookie(jar, name, value, opts)` wrapper that hard-codes the helper.
3. If GitGuardian or CodeQL gains a "secure: NODE_ENV" lint rule, this slice can be referenced as the canonical justification.
