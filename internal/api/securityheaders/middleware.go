// Package securityheaders applies the platform's HTTP hardening header
// set to every response served by the chi router built in
// internal/api/httpserver.go.
//
// Surfaced by slice 087 (filed from the 2026-Q2 security audit — see
// docs/audits/2026-Q2-security-audit.md, MEDIUM-HIGH finding). A grep
// across internal/ for Strict-Transport-Security, Content-Security-Policy,
// X-Frame-Options, X-Content-Type-Options, Referrer-Policy returned ZERO
// matches before this slice. The platform serves a web UI plus a
// multipart artifact upload endpoint, both of which carry standard
// browser-side attack surfaces (clickjacking, MIME-sniffing,
// MITM-on-first-visit, Referer leakage, XSS regression).
//
// The middleware is run as the FIRST root.Use(...) in httpserver.go,
// BEFORE the bearer-auth middleware, so unauthenticated responses
// (/login, /health, /v1/version, /v1/install-state, /auth/*) ALSO carry
// the hardening headers — those endpoints are public surfaces where a
// missing header is just as bad as on the authed dashboard.
//
// Header values + the CSP enforced-vs-report-only decision are documented
// in docs/audit-log/087-security-http-headers-middleware-decisions.md.
package securityheaders

import "net/http"

// CSP is the Content-Security-Policy directive set applied to every
// response. It is intentionally exported so tests can assert against the
// exact string and so future tighten-the-CSP slices have a single edit
// surface.
//
// Notes on the chosen directives:
//
//   - default-src 'self' — same-origin baseline; nothing loads cross-origin
//     without an explicit per-resource directive below.
//   - img-src 'self' data: — Tailwind / shadcn occasionally inline tiny
//     SVGs as data: URIs; allow them.
//   - style-src 'self' 'unsafe-inline' — Tailwind + shadcn inject inline
//     <style> blocks at runtime. 'unsafe-inline' is the documented
//     compromise (decisions log §D5). Hash/nonce migration is a future
//     slice once the inline surface stops mutating.
//   - script-src 'self' — strict; NO 'unsafe-inline' so Next.js inline
//     hydration scripts WILL violate the policy. That is the load-bearing
//     reason this slice ships in report-only mode (decisions log §D1) —
//     the browser console logs the violations, no script execution is
//     blocked, and the next slice tightens hydration via a nonce.
//   - font-src 'self' data: — same reasoning as img-src for inline
//     icon fonts that some shadcn components embed.
//   - frame-ancestors 'none' — clickjacking defense; redundant with
//     X-Frame-Options: DENY on modern browsers, but XFO covers older
//     ones (decisions log §D3).
//   - base-uri 'self' — defense-in-depth against <base href="..."> tag
//     injection redirecting relative URLs to an attacker origin.
//   - form-action 'self' — defense-in-depth against form-action
//     injection exfiltrating POSTed credentials to an attacker origin.
const CSP = "default-src 'self'; " +
	"img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; " +
	"font-src 'self' data:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// HSTSMaxAge is the Strict-Transport-Security max-age in seconds.
// One year (31536000) is the OWASP-recommended baseline. The directive
// includes includeSubDomains (no subdomain serves HTTP-only content
// today; decisions log §D2) but DOES NOT include preload — preload is
// irreversible (HSTS preload list) and an OSS project should not bind
// its deployers to a list they did not register on.
const HSTSMaxAge = "max-age=31536000; includeSubDomains"

// Middleware sets the five hardening headers on every response served
// through the chain. It writes the headers BEFORE calling next, so even
// if next short-circuits (e.g., bearer-auth returns 401), the response
// still carries the headers.
//
// The CSP is sent via Content-Security-Policy-Report-Only — see the
// package doc and decisions log §D1 for the trajectory toward enforced
// mode. The other four headers are uncontroversial and enforce
// immediately.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", HSTSMaxAge)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy-Report-Only", CSP)
		next.ServeHTTP(w, r)
	})
}
