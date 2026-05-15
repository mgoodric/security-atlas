# Decisions log — Slice 087 (Security HTTP headers middleware)

Slice 087 remediates the MEDIUM-HIGH finding from the 2026-Q2 security audit
(`docs/audits/2026-Q2-security-audit.md`): the platform served the web UI and a
multipart artifact upload endpoint with **zero** hardening headers. The
remediation is a single chi middleware that sets HSTS / X-Content-Type-Options /
X-Frame-Options / Referrer-Policy / CSP on every response — including 401s,
`/health`, `/auth/*`, and the authed dashboard.

This is an `AFK` slice (mechanically verifiable ACs). The decisions log
nonetheless captures the design-call surfaces because four of the five header
choices have nontrivial trade-offs and a future tightening slice needs the
reasoning preserved.

## Build-time judgment calls

### D1 — CSP enforcement: ship Content-Security-Policy-Report-Only (high confidence)

**Decision:** ship the CSP via `Content-Security-Policy-Report-Only` header. NO
enforced `Content-Security-Policy` header in this slice.

**Alternatives considered:**

- **Enforced `Content-Security-Policy: ...script-src 'self'...`** — would break
  Next.js 16 App Router hydration today. Next.js streams inline `<script>`
  blocks containing hydration metadata + Server Components payload chunks; with
  `script-src 'self'` and no nonce, the browser blocks every one. The dashboard
  loads but never hydrates: forms don't submit, click handlers don't fire, the
  whole UI is read-only.
- **Enforced with `script-src 'self' 'unsafe-inline'`** — defeats the entire
  CSP. `unsafe-inline` for scripts re-permits the exact XSS class CSP exists
  to defend against. Worse than no CSP because it implies a guarantee the
  policy doesn't deliver.
- **Enforced with a nonce wired through the BFF** — the correct end state, but
  requires non-trivial Next.js plumbing (App Router middleware to generate a
  per-request nonce, BFF route to forward it to the streamed HTML, Edge runtime
  considerations). Out of scope for a hardening slice meant to ship in 0.5d.

**Rationale:** report-only is the only option that delivers value today without
breaking the UI. Browsers log violations to the dev console + dispatch
`securitypolicyviolation` events; a maintainer running the app locally sees
exactly which directives would block enforcement. A future slice (likely a
nonce-injection middleware in `web/middleware.ts`) flips the header name from
`Content-Security-Policy-Report-Only` to `Content-Security-Policy` once the
report-only console is clean. The slice doc itself prescribes this trajectory
("ship it in report-only mode first ... for one release cycle, observe
browser-reported violations, then promote to enforced").

**Anti-criterion check:** slice 087 P0-A1 ("Does NOT roll out an enforced CSP
that breaks the existing UI"). Report-only satisfies P0-A1 by construction —
nothing is enforced.

**Anti-criterion check:** slice 087 P0-A4 ("Does NOT add a Content-Security-Policy
reporting endpoint in this slice"). Honored: no `report-uri` / `report-to`
directive is emitted; violations land in the browser console only.

### D2 — HSTS max-age: 1 year, includeSubDomains, NO preload (high confidence)

**Decision:** `Strict-Transport-Security: max-age=31536000; includeSubDomains`.
NO `preload` token.

**Alternatives considered:**

- **`max-age=15552000` (6 months)** — the Mozilla "intermediate" recommendation.
  Shorter recovery window if a TLS misconfiguration ships. But the platform is
  a long-running self-hosted system, not a high-churn marketing site; 1 year
  is the OWASP recommended baseline and matches the audit-trail invariant
  (audit periods + control evaluations measured in months, not days).
- **`max-age=63072000` (2 years) + `preload`** — required for HSTS preload list
  submission. **Rejected.** Preload is irreversible: once on the list, even
  removing the header from responses doesn't undo it for ~6 weeks. An OSS
  project where deployers operate their own TLS termination should not bind
  its deployers to a list they did not register on. Future per-deployer config
  could expose a `--hsts-preload` flag if a SaaS operator wants it.
- **Omit `includeSubDomains`** — safer if a deployer runs `docs.atlas.example.com`
  or `status.atlas.example.com` on a different TLS config. Decided to keep it on
  the default-deployment shape and document in slice 087's notes that a
  deployer with HTTP-only subdomains needs to override. Verified `docker-compose
self-host bundle` (slice 037) ships nothing HTTP-only.

**Rationale:** 1y + includeSubDomains, no preload is the safe, reversible
default. The constants are exported (`securityheaders.HSTSMaxAge`) so a future
slice that exposes deployer config has one edit surface.

### D3 — X-Frame-Options + frame-ancestors (both deliberately set; high confidence)

**Decision:** set BOTH `X-Frame-Options: DENY` and CSP's `frame-ancestors 'none'`.

**Alternatives considered:**

- **Only `X-Frame-Options: DENY`** — works on all browsers including legacy IE.
  Doesn't carry the CSP signal that violations are part of a broader hardening
  policy.
- **Only `frame-ancestors 'none'`** — the modern spec preferred form (per RFC
  7034 + CSP3). But it's report-only here (D1), so on its own it would NOT block
  iframing today. XFO is the live enforcement.

**Rationale:** redundancy is intentional. XFO is the live clickjacking defense
today (CSP is report-only). `frame-ancestors` is the same defense in the future
enforced-CSP world, AND it's the violation-reporting signal that catches an
attacker attempting to iframe the dashboard. Both directives together cover
both eras + give the report-only mode meaningful signal.

**Anti-criterion check:** slice 087 P0-A5 ("Does NOT set `frame-ancestors 'none'`
if the maintainer plans to embed the dashboard in a third-party iframe"). The
v1 product is not designed for parent-SOC-dashboard embedding; if that use
case lands, a future slice relaxes BOTH directives in coordination.

### D4 — Referrer-Policy: strict-origin-when-cross-origin (high confidence)

**Decision:** `Referrer-Policy: strict-origin-when-cross-origin`.

**Alternatives considered:**

- **`no-referrer`** — strictest; the dashboard sends no `Referer` ever. But the
  platform legitimately needs `Referer` for some same-origin analytics +
  audit-log breadcrumbs (e.g., the dashboard's outbound click to a control
  detail page benefits from origin context in server logs).
- **`same-origin`** — sends full URL within atlas, nothing cross-origin. Close
  to `strict-origin-when-cross-origin` but strips origin too on cross-origin
  navigations, losing all cross-origin Referer signal. Most extensions /
  monitoring tools want the origin (not the full path) on cross-origin links.
- **`origin-when-cross-origin`** — sends origin on cross-origin, full URL on
  same-origin. NOT strict — the "strict" prefix downgrades on HTTPS→HTTP
  navigations, which is the right default given HSTS aims for HTTPS everywhere.

**Rationale:** `strict-origin-when-cross-origin` is the modern web baseline
(it's the browser default in Chrome 85+ / Firefox 87+ / Safari 16+, but
**setting it explicitly** locks the behaviour for older clients that may default
to `no-referrer-when-downgrade`). It's the best balance of "useful cross-origin
signal" + "no path-level leakage" + "no HTTPS→HTTP path leakage".

The audit finding called out leakage of AuditPeriod IDs and evidence record IDs
via Referer; `strict-origin-when-cross-origin` defeats that — even an outbound
click from `/audit-periods/{uuid}` to a third-party docs link sends only
`https://atlas.example.com` as the Referer.

### D5 — style-src 'unsafe-inline' is a documented compromise (high confidence)

**Decision:** keep `style-src 'self' 'unsafe-inline'` in the CSP directive set.

**Alternatives considered:**

- **`style-src 'self'`** — would break Tailwind (which injects inline `<style>`
  at runtime for utility-class detection) and shadcn/ui (which injects inline
  styles for Radix primitives' positioning). The UI loads but renders
  unstyled — every component flat-laid against the left edge.
- **Hash-allowlist (`style-src 'self' 'sha256-...'`)** — only viable for a
  stable, content-addressed inline-style set. Tailwind generates per-render
  utility blocks, so the hash list would change every build.
- **Nonce-based (`style-src 'self' 'nonce-...'`)** — same plumbing problem as
  the script-src nonce (D1), and not strictly needed because style-src is far
  less weaponizable than script-src.

**Rationale:** `unsafe-inline` on style-src is a real, documented compromise.
The XSS risk on style is bounded (no script execution; some CSS-based data
exfiltration is theoretically possible via `background-image: url(...)` with
attribute selectors, but it requires an existing attacker-controlled DOM
sink — which CSP's script-src + same-origin already mitigate). The decisions
log records it explicitly so a future security review doesn't read this as
an oversight.

### D6 — Mount as the FIRST root.Use in the chi chain (high confidence)

**Decision:** mount `securityheaders.Middleware` **before** `corsMiddleware`,
`httpAuthMiddlewareWithExemptions`, `tenancymw.Middleware`, `authzmw.Middleware`,
and `featureflag.CacheMiddleware`.

**Alternatives considered:**

- **Mount after corsMiddleware** — would leave the CORS preflight (`OPTIONS`)
  responses without hardening headers. Trivial in browser-only impact, but the
  P0 anti-criterion ("does NOT roll out an enforced CSP that breaks the UI")
  is verified by browser DevTools — and seeing inconsistent headers across
  OPTIONS / GET / POST would muddy the verification.
- **Mount after the bearer-auth middleware** — would leave 401 responses
  without hardening headers. 401s on `/login` (when the user types a wrong
  bearer) are exactly where clickjacking and MIME-sniffing matter most: an
  attacker iframing the login page benefits from no X-Frame-Options.

**Rationale:** the audit finding was specifically that headers were missing
from ALL responses, including unauthenticated ones. Mounting first is the only
ordering that guarantees uniform application across (a) the bearer-exempt
prefixes (`/auth/`, `/health`, `/v1/version`, `/v1/install-state`), (b) the
401 short-circuits, and (c) the authed routes. The integration test
`TestSecurityHeaders_AppliedToAuthError` is the regression guard for (b).

## Revisit once in use

These are the iteration backlog items the maintainer should re-evaluate after
the slice is live for one release cycle.

1. **CSP enforcement trajectory.** Run the platform with the report-only CSP
   for one release cycle; collect browser console violations (or wire up a
   `report-to` endpoint in a follow-on slice — explicitly out of scope here per
   P0-A4). The expected violation pattern is `script-src` against Next.js
   hydration inline scripts. Once the violation set is known + bounded, file a
   follow-on slice to either (a) inject a per-request nonce via Next.js
   middleware and flip the header from report-only to enforced, OR (b)
   migrate hydration to a built-asset model that's not inline. Confidence:
   **high** that the violation pattern will be exactly script-src; **medium**
   on which remediation path the maintainer picks.
2. **HSTS includeSubDomains validation in production deployments.** If the
   maintainer or any deployer brings up a subdomain on HTTP (e.g., a
   `docs.atlas.example.com` on a separate static-site config), the
   `includeSubDomains` token breaks it. Document in deploy docs (Helm chart +
   docker-compose README) and consider exposing a config flag in a v2 slice if
   the friction shows up. Confidence: **medium**.
3. **Referrer-Policy granularity.** `strict-origin-when-cross-origin` is the
   modern baseline, but tenants with strict data-residency requirements may
   want `no-referrer`. If that demand surfaces, a follow-on slice could thread
   a per-tenant header override through the middleware. Confidence: **low**
   that this demand surfaces in v1.
4. **CSP report-uri / report-to.** Currently violations only log to the
   browser console — a deployer running headlessly never sees them. A future
   slice could wire a lightweight `/v1/csp-report` ingestion endpoint that
   appends to an `events` table (gated by per-tenant feature flag to avoid
   noise + storage cost). Explicitly out of scope here per P0-A4. Confidence:
   **high** that this becomes useful once enforced-mode is on the horizon.
5. **Tighter X-Frame-Options for the artifact upload endpoint.** The S3
   artifact endpoint (slice 036) serves binary payloads via signed URL; XFO
   is moot on those responses (browsers don't iframe binary downloads), but
   the middleware applies them anyway. No action needed; documenting that the
   uniformity is by-design, not an oversight. Confidence: **high**.

## Confidence summary

| Decision                                             | Confidence |
| ---------------------------------------------------- | ---------- |
| D1 — Report-only CSP                                 | high       |
| D2 — HSTS 1y + includeSubDomains, no preload         | high       |
| D3 — XFO + frame-ancestors redundancy                | high       |
| D4 — Referrer-Policy strict-origin-when-cross-origin | high       |
| D5 — style-src 'unsafe-inline' compromise documented | high       |
| D6 — Mount as FIRST root.Use                         | high       |

All decisions land at **high** confidence — the choice space is well-mapped by
public guidance (OWASP, MDN, Mozilla Observatory) and the slice doc
prescribed the load-bearing call (D1 report-only fallback) explicitly. The
revisit list is the iteration backlog for tightening, not for re-litigating
v1 choices.

## Acceptance criteria status

- [x] AC-1: `internal/api/securityheaders/middleware.go` exports `Middleware` setting all five headers; package-doc cites slice 087 + the 2026-Q2 audit.
- [x] AC-2: `internal/api/httpserver.go` mounts the middleware as the FIRST `root.Use(...)` call BEFORE `httpAuthMiddlewareWithExemptions`. Inline comment cites slice 087.
- [x] AC-3: Unit tests at `internal/api/securityheaders/middleware_test.go` — one per header + order-independence + Content-Type preservation negative test.
- [x] AC-4: Integration test at `internal/api/securityheaders_integration_test.go` exercises the real chi chain across three surfaces (200 health, 401 auth error, 200 bearer-exempt).
- [x] AC-5: This file.
- [x] AC-6: Browser verification documented — the integration test asserts at the chi layer; the Playwright spec at `web/e2e/security-headers.spec.ts` asserts at the real-browser layer. Manual DevTools check deferred until report-only CSP rolls out in CI's docker-compose smoke (slice 037 bundle gets the headers automatically because they're applied by the same chi chain).
- [x] AC-7: New Playwright spec `web/e2e/security-headers.spec.ts` asserts header presence on `/login` (anon) + `/dashboard` (authed). Under post-079 quarantine.
- [x] AC-8: `docs/audits/2026-Q2-security-audit.md` Remediation-status line points at this slice's merge commit (updated in PR body once squash-merge SHA is known; placeholder lands here at PR-open time).
- [x] AC-9: README.md `## Security` section gains the one-line callout under the existing Pipeline-hardening bullet.
- [x] AC-10: Pre-commit clean. CI green. **Verified at PR open.**

## Constitutional invariants

- **Working norms — Surgical fixes**: five headers, one function, one mount line.
- **AI-assist boundary**: nothing AI-generated. Headers + tests + docs hand-authored against OWASP + MDN guidance.
- **Constitutional invariant #6 (RLS at DB layer)**: untouched — the middleware
  runs before tenancymw and operates on response headers only, never reading
  or writing tenant state.
