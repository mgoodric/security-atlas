// Slice 086 — open-redirect helper for post-login redirect targets.
//
// The `signIn` server action (web/app/login/actions.ts) reads a `from`
// form field, originating from `/login?from=...`, and previously passed
// it straight to Next.js `redirect()`. That enabled phishing pivots via
// `?from=https://evil.example.com/phish` — flagged HIGH in the
// 2026-Q2 security audit (`docs/audits/2026-Q2-security-audit.md`).
//
// This helper is the single point of validation for any redirect target
// pulled from user input. Three checks + fallback:
//
//   1. Must start with `/`         (rejects fully-qualified URLs,
//                                   `javascript:` URLs, data: URLs,
//                                   bare strings, etc.)
//   2. Must not start with `//`    (rejects protocol-relative URLs,
//                                   which browsers resolve against the
//                                   current scheme)
//   3. Must not start with `/\`    (rejects backslash injection paths
//                                   that some parsers normalize)
//   4. Must not be exactly `/`     (the root path lands on a non-
//                                   `/dashboard` route; should not
//                                   bypass the explicit default)
//
// Anything else falls back to `/dashboard`, the established post-login
// destination. The helper is intentionally short — a 30-line URL
// sanitizer would invite edge-case regression; the test set
// (web/lib/safe-redirect.test.ts) is the gate that keeps it short.
//
// Reviewer-discipline: all redirect targets sourced from user input
// MUST flow through `safeRedirectTarget` before reaching `redirect()`
// / `NextResponse.redirect()` / `router.push()`. See CONTRIBUTING.md
// "Open-redirect prevention" for the convention.
export function safeRedirectTarget(target: string): string {
  if (!target.startsWith("/")) return "/dashboard";
  if (target.startsWith("//")) return "/dashboard";
  if (target.startsWith("/\\")) return "/dashboard";
  if (target === "/") return "/dashboard";
  return target;
}
