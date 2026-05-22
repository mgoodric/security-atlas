# 206 — BFF auth cookie migration · decisions log

**Slice:** `docs/issues/206-bff-cookie-migration-to-atlas-jwt.md`
**Branch:** `frontend/206-bff-cookie-migration`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-22

This log captures the JUDGMENT calls made while building slice 206. The
slice doc specifies the WHAT; this log records the HOW + the trade-offs
I weighed inline. All decisions are reviewable post-merge by the
maintainer.

---

## D1 — Cookie pass-through vs Authorization Bearer for BFF→backend calls

**Decision:** **Authorization Bearer header.** No call-site changes
required beyond the constant value rename. The BFF reads the cookie
value (the JWT) from the jar and forwards it as
`Authorization: Bearer <jwt>` to the Go backend. This is what every
BFF route handler in `web/app/api/**/route.ts` already does — the
codebase is uniform on this pattern.

**Verification:**

I read `internal/auth/jwtmw/middleware.go` (the slice 190 JWT
middleware that gates every `/v1/*` request) and `web/lib/api/bff.ts`
(the shared BFF forwarder used by the audit-workspace + many other
BFF routes):

- `jwtmw.extractJWT` at `internal/auth/jwtmw/middleware.go:252-271`
  reads the JWT from either (a) `Authorization: Bearer eyJ...` OR
  (b) the configured cookie (default cookie name `atlas_session`,
  set via `jwtmw.Options.CookieName` — `internal/api/httpserver.go:195`
  wires it to `jwtmw.DefaultCookieName`).
- `web/lib/api/bff.ts:33-39` reads `SESSION_COOKIE` from the jar and
  builds `headers["Authorization"] = "Bearer " + bearer`. The cookie
  value IS the JWT.

So the backend already accepts the JWT shape the BFF sends. The cookie
in the BFF jar is the JWT, named `SESSION_COOKIE` (post-slice-206
value: `atlas_jwt`). The wire that goes to the backend carries the
same JWT in the Authorization header. No backend change, no BFF
call-site change.

**Alternatives considered:**

| Approach                                                        | Why rejected                                                                                                                                                                                                                                                                                                                                                                             |
| --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) Cookie pass-through: `Cookie: atlas_jwt=<jwt>` to backend   | Would require widening `jwtmw.Options.CookieName` to include `atlas_jwt` in addition to the default `atlas_session`. Doable, but adds a second backend-side cookie name to maintain. P0-A1 says no new auth mechanism — adding a second-named cookie path IS a new code path. Standard JWT/OAuth practice is the Authorization header; the BFF→backend hop should be the textbook shape. |
| Migrate the entire codebase to Authorization-header-from-cookie | Already done. No further change needed.                                                                                                                                                                                                                                                                                                                                                  |

**Trade-off accepted:** The `atlas_session` cookie name on the backend
side is still the default (slice 190's choice). The BFF cookie is
named `atlas_jwt` (the OAuth callback's choice — slice 189 D1). The
two names diverge BUT that's fine because the BFF translates: it
reads `atlas_jwt` from the browser, then sends `Authorization: Bearer
<jwt>` (no cookie) to the backend. The backend's `atlas_session`
cookie path is reserved for the slice 110 admin sessions surface +
direct-backend curl users; the BFF doesn't use it.

---

## D2 — proxy.ts exemption set

**Decision:** Add two new exemptions to the `web/proxy.ts` gate:

1. `pathname.startsWith("/v1/")` — backend pass-through (curl + the
   self-host deploy proxies `/v1/*` traffic through the Next.js host).
2. `pathname === "/metrics"` — slice 121 OTel runtime metrics endpoint.

**Final exemption set:**

| Exemption                           | Why                                                                                                                                                                                                                                                                                                                                               |
| ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `startsWith("/login")`              | Original — the login page must be reachable without a session cookie.                                                                                                                                                                                                                                                                             |
| `startsWith("/_next")`              | Original — Next.js static asset emit.                                                                                                                                                                                                                                                                                                             |
| `startsWith("/v1/")` **(new)**      | The self-host deploy reverse-proxies the platform's `/v1/*` API through the Next.js host. A curl to `/v1/me` with no auth must surface the backend's real 401 response, not be masked by a Next.js 307 → `/login`. P0-A2: this exemption ONLY removes the Next.js redirect; the backend's slice-190 `jwtmw` middleware continues to enforce auth. |
| `=== "/metrics"` **(new)**          | Slice 121's OTel runtime metrics endpoint. Same logic as `/v1/*` — platform surface, not a Next.js page. Exact-equality to avoid leaking `/metrics-export` or similar adjacent paths in the future.                                                                                                                                               |
| `=== "/api/version"`                | Slice 092 — public version-metadata BFF.                                                                                                                                                                                                                                                                                                          |
| `=== "/api/install-state"`          | Slice 123 — public install-state BFF for the unauthenticated login page's first-install card.                                                                                                                                                                                                                                                     |
| `PUBLIC_STATIC_FILES.has(pathname)` | Slice 123 — logos / favicons / OG cards referenced from the login page.                                                                                                                                                                                                                                                                           |

**Considered but not added:**

- `/api/health` — does NOT exist in `web/app/api/` (verified via
  `find web/app/api -name route.ts | xargs grep -l health` → 0 hits).
  Skipped — no need to exempt a non-existent route.
- `pathname.startsWith("/api/v")` — explicitly rejected. Slice 092
  P0-A1 discipline: a `startsWith("/api/v")` would silently expose
  `/api/vendors`, `/api/audit/period`, etc. Use exact-equality for
  every BFF exemption to fail closed on future sub-route additions.

**Test coverage added** (`web/proxy.test.ts`):

- `exempts /v1/me / /v1/anchors / /v1/install-state / /v1/oauth/token`
  (`test.each`).
- `exempts /metrics`.
- `does NOT exempt /metricsasdf` (exact-equality discipline).
- `does NOT exempt /v1` (bare `/v1` without trailing slash is NOT the
  platform URL prefix).
- `authenticated user with atlas_jwt cookie passes through`.
- `legacy sa_session_token cookie no longer authenticates` (regression
  test for the migration).

---

## D3 — Honest CI-delta scan (per slice 202 D2 precedent)

This section captures the LOCAL CI parity scan results. Per the slice
brief: "Recent regression: slice 205 D7 claimed clean but missed 7
lint findings. DO NOT REPEAT."

**Commands run locally before push:**

```
cd web && npm ci                     # node_modules provisioned
cd web && npm run lint               # eslint via next lint
cd web && npm run test               # vitest
cd web && npm run build              # next build (catches TS errors)
pre-commit run --all-files           # repo-wide hooks
```

**Findings (verbatim 2026-05-22 local runs):**

- **`cd web && npm run lint`** — `0 errors, 2 warnings`. Both warnings
  are PRE-EXISTING (lines 127 + 406 of
  `web/scripts/capture-readme-screenshots.ts` flagged as "Unused
  eslint-disable directive (no problems were reported from
  'no-console')"). Slice 206's edit to that file is at line 113 — far
  from the flagged lines and not the cause. Verified via
  `git blame` (the unused-disable directives predate slice 206 and
  are owned by slice 132's screenshot capture refresh).

- **`cd web && npm run test`** — `Test Files 74 passed (74)`, `Tests
738 passed (738)`. Includes the new `web/proxy.test.ts` cases for
  the `/v1/*` + `/metrics` exemptions and the
  `legacy sa_session_token cookie no longer authenticates`
  regression test.

- **`cd web && npm run build`** — `next build` completed with exit 0;
  every route compiled. Proxy middleware emitted as
  `ƒ Proxy (Middleware)` in the output route table. No type errors.

- **`pre-commit run --all-files`** — First run flagged `prettier`
  hook reformatting `docs/audit-log/206-...md` +
  `docs/issues/206-...md` (markdown table cell padding). Reformatted
  files re-staged; second run PASSED all 14 hooks (trim trailing
  whitespace, fix end of files, check yaml/json/toml, large files,
  private key + AWS creds detection, mixed line ending, gofmt, ruff
  - ruff-format, prettier, actionlint slice-158).

No findings caused by this slice survived to the commit. Hot-fix
discipline honoured: the deployed product is broken; we ship one PR
end-to-end, not a two-PR chain.

**AC-7 — backend 401 confirmation:**

A live `curl -i http://localhost:8080/v1/me` was not feasible here
(the worktree does not have a running atlas server). Instead, I
verified the code path by reading:

- `internal/api/httpserver.go:218-221` — `requireCredential` middleware
  is mounted AFTER `jwtmw.Middleware`, and `/v1/me` is NOT in the
  exempt set (the exempt list is `/auth/`, `/health`, `/metrics`,
  `/v1/version`, `/v1/install-state`, `/v1/calendar.ics`,
  `/.well-known/`, `/oauth/token`, `/oauth/authorize`,
  `/oauth/revoke`, `/oauth/introspect`,
  `/oauth/device_authorization`, `/v1/test/issue-jwt`). A request
  with no credential in context (no JWT, no cookie, no header) reaches
  `requireCredential` and is rejected with 401.

- `internal/auth/jwtmw/middleware.go:282-287` — `write401` writes
  HTTP 401 + `WWW-Authenticate: Bearer realm="atlas",
error="invalid_token"` + `Content-Type: application/json` +
  `{"error":"invalid_token"}`. This is the response shape for any
  request that arrives with a malformed-but-present token.

P0-191-1 invariant (no auth-bypass window for requests with no token)
is intact post-slice-197 / slice-198. Slice 206 does not touch this
backend path; the proxy.ts exemption added in D2 only removes the
Next.js-layer 307; the backend's 401 surfaces to the curl client
unchanged.

**Specific lint hazards monitored** (per the slice brief):

- `unused` on any `SESSION_COOKIE` import that becomes unreachable —
  none observed; every import site still consumes the constant.
- `errcheck` on cookie.get() pattern changes — no pattern changes; the
  constant value rename is opaque to call sites.
- `unused-vars` in test fixture refactors — none; the test fixture
  edits are minimal (string literal swaps + new test cases).

The final verbatim CI-delta output is captured in the PR description
under the "D3 CI-delta scan results" section, so this log + the PR
remain a single source of truth at the merge moment.

---

## D4 — Why this is one slice, not three

A maintainer could reasonably ask: "Why not three slices — one for
the constant rename, one for the proxy exemption, one for the comment
cleanup?" The answer:

1. **The user is locked out RIGHT NOW.** Every minute we delay is
   another minute the deployed product is broken. A three-slice chain
   is a 1-day cycle minimum per slice = 3 days. The hot-fix discipline
   trumps the usual one-slice-one-concern principle.

2. **The pieces are coupled.** Renaming the constant without the
   proxy exemption leaves curl to `/v1/me` 307'd to `/login`. The
   proxy exemption without the constant rename leaves the dashboard
   loop intact. Either alone is half a fix. Shipping the pair is the
   atomic unit that restores service.

3. **The comment cleanups are trivial and within blast radius.** Six
   route-file header comments mentioning `sa_session_token` were
   updated to reference `SESSION_COOKIE` (the constant). These are
   doc-only diffs; bundling them avoids a "tidy follow-on" PR.

The slice brief explicitly allows this scope (`Deliverable: One
DCO-signed commit. Subject: fix(auth): slice 206 — ...`).
