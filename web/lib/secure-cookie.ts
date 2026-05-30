// Slice 146 — pick the `Secure` cookie attribute based on the actual
// request transport, not the build-time `NODE_ENV`.
//
// Why this exists (the bug):
//
//   Before slice 146 the `signIn` server action used
//   `secure: process.env.NODE_ENV === "production"` when calling
//   `cookies().set(ATLAS_JWT_COOKIE, ...)`. That set the `Secure`
//   attribute on every production-build deployment — INCLUDING the
//   many self-hosted operators (Unraid, docker-compose without TLS,
//   `node .next/standalone/server.js` smoke runs) that serve the app
//   over plain HTTP. Browsers refuse to send `Secure` cookies over
//   HTTP, so the cookie never came back to the BFF. `web/proxy.ts`
//   then saw no `ATLAS_JWT_COOKIE` on `/api/dashboard/**` (and every
//   other BFF path), redirected to `/login`, and the browser's BFF
//   fetch followed the redirect and tried to JSON.parse the login
//   HTML — the "Could not load this panel · Unexpected token '<'"
//   symptom captured in `docs/audit-log/132-readme-refresh-decisions.md`
//   D5 + the README of this slice (docs/issues/146-*).
//
// Why we trust headers, not NODE_ENV:
//
//   The transport the browser used is a per-request property, not a
//   build-time property. Two production-build deployments can differ:
//
//     A) docker-compose default (no TLS)   → plain HTTP → Secure must be FALSE
//     B) Helm with cert-manager ingress    → HTTPS via XFP=https → Secure TRUE
//
//   The standard signal a reverse proxy uses to inform us is the
//   `X-Forwarded-Proto` header (de-facto standard) or the
//   `Forwarded` header (RFC 7239). Both are honored.
//
// Default-INSECURE rationale:
//
//   When no proto signal is present we treat the request as plain HTTP
//   and emit `secure: false`. The dominant case is a self-host operator
//   on plain HTTP without a reverse proxy header rewrite. Any HTTPS
//   deployment behind any sane reverse proxy (nginx, Traefik, NPM,
//   Cloudflare, AWS ALB, K8s ingress controllers) sets
//   `X-Forwarded-Proto: https` by default, so case (1) catches them.
//   The alternative — default-Secure — silently re-introduces the
//   regression this slice fixes.
//
// Scope discipline (slice 146 P0-COOKIE-1):
//
//   This helper is ONLY for picking the `Secure` Set-Cookie attribute
//   at session-cookie creation time. It is NOT a Cookie-header
//   construction helper (that responsibility belongs to slice 110's
//   `buildSessionsForwardHeaders` for the /api/me/sessions* routes).
//   Do not broaden the surface.

const HTTPS_LITERAL = "https";

// shouldUseSecureCookie returns true when the request reached us over
// HTTPS, according to the standard reverse-proxy signals. Returns
// false when the request was plain HTTP OR no signal is present
// (default-INSECURE; see file header for rationale).
export function shouldUseSecureCookie(headers: Headers): boolean {
  // 1. De-facto-standard X-Forwarded-Proto header. Set by every
  //    credible reverse proxy by default. Case-insensitive compare.
  const xfp = headers.get("x-forwarded-proto");
  if (xfp !== null) {
    return xfp.trim().toLowerCase() === HTTPS_LITERAL;
  }

  // 2. RFC 7239 Forwarded header. Format: `for=...;proto=https;by=...`
  //    (semicolon-separated key=value pairs, optionally comma-separated
  //    for chained proxies). We parse the first `proto=` occurrence —
  //    the most adjacent proxy.
  const forwarded = headers.get("forwarded");
  if (forwarded !== null) {
    const m = /proto=([A-Za-z]+)/i.exec(forwarded);
    if (m) {
      return m[1].toLowerCase() === HTTPS_LITERAL;
    }
  }

  // 3. No signal — default to NOT-secure so the cookie round-trips on
  //    plain-HTTP self-host deployments. HTTPS deployments will have
  //    case (1) or (2) above.
  return false;
}
