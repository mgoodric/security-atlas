// Slice 086 — open-redirect helper for post-login redirect targets.
//
// The `signIn` server action (web/app/login/actions.ts) reads a `from`
// form field, originating from `/login?from=...`, and previously passed
// it straight to Next.js `redirect()`. That enabled phishing pivots via
// `?from=https://evil.example.com/phish` — flagged HIGH in the
// 2026-Q2 security audit (`docs/audits/2026-Q2-security-audit.md`).
//
// This helper is the single point of validation for any redirect target
// pulled from user input. Checks, any failure falling back to `/dashboard`:
//
//   1. Must start with `/`, and not `//` or `/\`  (rejects absolute,
//                                   protocol-relative, `javascript:`,
//                                   data: and backslash-injection targets)
//   2. No control characters        (#2196: browsers strip TAB, LF and CR
//                                   from URLs, so `/\t/evil.com` in a
//                                   Location header becomes `//evil.com`)
//   3. Parsed against a fixed base, it stays on that origin and its path
//      does not start with `//`     (catches whatever a URL parser
//                                   resolves off-site that the prefix
//                                   checks miss, e.g. `/.//evil.com`)
//   4. Must not be exactly `/`      (the root path lands on a non-
//                                   `/dashboard` route; should not
//                                   bypass the explicit default)
//
// `/dashboard` is the established post-login destination. The test set
// (web/lib/safe-redirect.test.ts) is the gate.
//
// Reviewer-discipline: all redirect targets sourced from user input
// MUST flow through `safeRedirectTarget` before reaching `redirect()`
// / `NextResponse.redirect()` / `router.push()`. See CONTRIBUTING.md
// "Open-redirect prevention" for the convention.
const FALLBACK = "/dashboard";
const PARSE_BASE = "http://atlas.invalid";
const CONTROL_CHARS = /[\u0000-\u001f\u007f]/;

export function safeRedirectTarget(target: string): string {
  if (!target.startsWith("/")) return FALLBACK;
  if (target.startsWith("//") || target.startsWith("/\\")) return FALLBACK;
  if (CONTROL_CHARS.test(target)) return FALLBACK;
  if (target === "/") return FALLBACK;
  let parsed: URL;
  try {
    parsed = new URL(target, PARSE_BASE);
  } catch {
    return FALLBACK;
  }
  if (parsed.origin !== PARSE_BASE) return FALLBACK;
  if (parsed.pathname.startsWith("//")) return FALLBACK;
  return target;
}
