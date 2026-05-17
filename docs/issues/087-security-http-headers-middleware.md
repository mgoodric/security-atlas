# 087 — Security HTTP headers middleware

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK

## Narrative

Surfaced by the 2026-Q2 security audit (slice 085). **MEDIUM-HIGH severity finding.**

`grep` across `internal/` for `Strict-Transport-Security`, `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy` returns **zero matches**. The platform serves a web UI (slices 005 + 040 + 041 + 042 + 043 + 056 + 060 + 063 + 072 + 073) and a multipart artifact upload endpoint (slice 036) without any of the standard hardening headers.

Specific gaps:

- **No HSTS** — first-visit MITM downgrade. A user typing `atlas.example.com` (without `https://`) hits HTTP first; an active network attacker can pivot to a non-HTTPS clone before any redirect to HTTPS lands.
- **No CSP** — XSS payloads execute with full origin privileges. Slice 042 had a dismissed CodeQL `js/xss-through-dom` finding (React-escaped); CSP is defense-in-depth against future regressions.
- **No X-Frame-Options / `frame-ancestors`** — clickjacking on authenticated sessions: attacker iframes the dashboard + overlays transparent UI + captures clicks.
- **No X-Content-Type-Options: nosniff** — MIME-confusion on uploaded artifacts: malicious upload claiming `text/plain` containing `<script>` could be sniffed as HTML.
- **No Referrer-Policy** — sensitive URLs (AuditPeriod IDs, evidence record IDs) leak via `Referer` when users click outbound links from the dashboard.

Fix: a single `security-headers` middleware run as the FIRST `root.Use(...)` in `internal/api/httpserver.go`, before the bearer-auth middleware (so even unauthenticated responses like `/login` carry the hardening headers).

**Recommended header set (engineer's grill confirms or tunes; record in decisions log):**

```go
// internal/api/securityheaders/middleware.go (new package)
func Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        w.Header().Set("Content-Security-Policy",
            "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
                "script-src 'self'; font-src 'self' data:; "+
                "frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
        next.ServeHTTP(w, r)
    })
}
```

The CSP needs care — Next.js generates inline `<script>` for hydration metadata (varies per Next major; needs a nonce or hash, OR allow `'unsafe-inline'` for `script-src` initially). The engineer's grill verifies against the actual built `/login` page + `/dashboard` page. If the CSP breaks the UI, ship it in **report-only mode first** (`Content-Security-Policy-Report-Only`) for one release cycle, observe browser-reported violations, then promote to enforced.

## Acceptance criteria

- [ ] AC-1: New package `internal/api/securityheaders/` with `Middleware` function setting the five headers (HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, CSP). Package-doc comment cites slice 087 + the 2026-Q2 audit.
- [ ] AC-2: `internal/api/httpserver.go` mounts `securityheaders.Middleware` as the first `root.Use(...)` call, BEFORE `httpAuthMiddlewareWithExemptions`. Inline comment cites slice 087.
- [ ] AC-3: Unit tests at `internal/api/securityheaders/middleware_test.go` verify each header is set on a sample 200 response. One test per header. Plus one test verifying the middleware is order-independent (works regardless of subsequent handler chain).
- [ ] AC-4: Integration test extending `internal/api/httpserver_test.go` (or a new file): make a real GET to `/health` + `/login` + an authed endpoint, assert all five headers appear in each response.
- [ ] AC-5: A `docs/audit-log/087-security-http-headers-middleware-decisions.md` records: (1) CSP enforcement vs report-only choice (default: enforced, but fall back to report-only if Next.js inline-script hydration is detected), (2) exact CSP directives chosen (verbatim copy), (3) HSTS max-age choice (default: 1 year), (4) X-Frame-Options vs `frame-ancestors` redundancy (both are set deliberately — older browsers honor only XFO).
- [ ] AC-6: Manual browser test against a running local stack: load `/login`, `/dashboard` (with bearer), open browser dev tools, verify all five headers in the Network panel. Engineer records the verification screenshot path or note in the decisions log.
- [ ] AC-7: A new Playwright spec at `web/e2e/security-headers.spec.ts` (slice 069 runner; under post-079 quarantine) asserts all five headers are present on the `/login` and `/dashboard` responses. Header-presence assertion only — value-validation is overkill for e2e.
- [ ] AC-8: `docs/audits/2026-Q2-security-audit.md` Remediation-status line under the MEDIUM-HIGH finding points at this slice's merge commit.
- [ ] AC-9: README.md "Security" section gains a one-line "Hardening headers: HSTS / CSP / X-Frame-Options / X-Content-Type-Options / Referrer-Policy applied on every response. See `internal/api/securityheaders/`."
- [ ] AC-10: Pre-commit clean. CI green.

## Constitutional invariants honored

- **Working norms — Surgical fixes**: smallest viable middleware. Five headers, one function, one mount line.
- **AI-assist boundary**: nothing AI-generated. Headers + tests + docs.

## Canvas references

- _(none — operational hardening; canvas doesn't speak to HTTP headers)_

## Dependencies

- All currently-merged frontend slices (the headers apply to their response paths)
- **069** (verification suite, merged) — Playwright runner AC-7 uses

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT roll out an enforced CSP that breaks the existing UI. If Next.js hydration requires inline scripts, ship report-only first OR use a nonce strategy. Verify in a browser before claiming AC-2 done.
- **P0-A2**: Does NOT include any header that requires per-tenant configuration. The middleware applies the SAME headers to every response; tenant-specific CSP would be a different design.
- **P0-A3**: Does NOT relax SameSite or any cookie flag as a side effect. Cookie flags are set by `internal/auth/sessions/sessions.go` + `internal/auth/oidc/oidc.go`; unchanged by this slice.
- **P0-A4**: Does NOT add a Content-Security-Policy reporting endpoint in this slice. Report-only mode (if chosen) reports to the browser console only; setting up a report-uri receiver is a separate slice.
- **P0-A5**: Does NOT set `frame-ancestors 'none'` if the maintainer plans to embed the dashboard in a third-party iframe (e.g., for a parent SOC dashboard). Engineer's grill confirms no such use case before applying.

## Skill mix (3–5)

- HTTP middleware in chi (existing Mount-append pattern)
- CSP authoring + Next.js hydration scripts (the trickiest part of this slice)
- `security-review` (header policy choices)
- vitest + Playwright + Go integration testing
- `simplify` (the middleware is one function; the docs are one line)

## Notes for the implementing agent

- **CSP is the load-bearing decision.** The other four headers are uncontroversial; CSP can break the UI in subtle ways. Test in a real browser against a real stack BEFORE committing. If you can't verify visually, ship report-only.
- **Don't try to perfect the CSP in v1.** A working report-only CSP is better than a broken enforced one. The decisions log captures the trajectory; future slices tighten directives.
- **`unsafe-inline` for `style-src`** is a known compromise for Tailwind. Document it in the decisions log so it doesn't read as an oversight.
- **HSTS `includeSubDomains`** is the right default but verify no subdomain serves HTTP-only content (e.g., a docs subdomain on a different SSL config). If unsure, ship without `includeSubDomains` first.
- After this lands, the dependency-vulnerability scanning slice (089) becomes more impactful — fewer attack vectors AND fewer known-CVE versions running.
