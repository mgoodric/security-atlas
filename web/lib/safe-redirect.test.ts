// Slice 086 — vitest coverage for the open-redirect helper.
//
// The function under test (`safeRedirectTarget`) validates post-login
// redirect targets pulled from the `?from=` search param / form field.
// It is the load-bearing defense against the HIGH finding in the
// 2026-Q2 security audit (`docs/audits/2026-Q2-security-audit.md`):
// `web/app/login/actions.ts` previously passed the user-supplied
// `from` value straight to Next.js `redirect()`, enabling phishing
// pivots via `?from=https://evil.example.com/phish`.
//
// The contract:
//   * Return the input unchanged when it looks like a safe relative
//     path (starts with `/`, second character not `/` or `\`, not
//     exactly `/`).
//   * Return `/dashboard` (the established post-login default) for
//     anything else — fully-qualified URLs, protocol-relative URLs,
//     backslash-prefixed paths, `javascript:` URLs, empty strings,
//     and bare `/`.
//
// The nine cases below mirror AC-3 of slice 086 verbatim. They are the
// exhaustive enumeration of attack + safe variants from the audit
// report; the helper STAYS three checks because the test set is the
// gate, not a permissive English description of "looks like a path".

import { describe, expect, test } from "vitest";

import { safeRedirectTarget } from "./safe-redirect";

describe("safeRedirectTarget", () => {
  // (a) Default destination passes through unchanged.
  test("passes through /dashboard", () => {
    expect(safeRedirectTarget("/dashboard")).toBe("/dashboard");
  });

  // (b) Deep relative path with an id passes through unchanged.
  test("passes through /risks/123", () => {
    expect(safeRedirectTarget("/risks/123")).toBe("/risks/123");
  });

  // (c) Fully-qualified attacker URL — blocked.
  test("rejects fully-qualified https URL", () => {
    expect(safeRedirectTarget("https://evil.com/phish")).toBe("/dashboard");
  });

  // (d) Protocol-relative URL — blocked.
  // Without scheme, browsers interpret `//evil.com` against the current
  // page scheme, navigating to `https://evil.com`. The helper rejects
  // anything starting with `//`.
  test("rejects protocol-relative URL", () => {
    expect(safeRedirectTarget("//evil.com/phish")).toBe("/dashboard");
  });

  // (e) javascript: URL — blocked.
  // Although the bare-path check (`!startsWith('/')`) already catches
  // this, the case is listed explicitly per the audit report's variant
  // catalog.
  test("rejects javascript: URL", () => {
    expect(safeRedirectTarget("javascript:alert(1)")).toBe("/dashboard");
  });

  // (f) Backslash-prefixed path — blocked.
  // Some path-parsers normalize `\\` to `//`; defense-in-depth reject.
  // `"\\\\evil.com\\path"` is the 12-character string `\\evil.com\path`.
  test("rejects backslash-prefixed path", () => {
    expect(safeRedirectTarget("\\\\evil.com\\path")).toBe("/dashboard");
  });

  // (g) Empty string — blocked (falls back to default).
  test("rejects empty string", () => {
    expect(safeRedirectTarget("")).toBe("/dashboard");
  });

  // (h) Root path alone — blocked.
  // The bare `/` lands on the marketing/dashboard root and should not
  // bypass the explicit `/dashboard` default. See decisions log D1.
  test("rejects bare /", () => {
    expect(safeRedirectTarget("/")).toBe("/dashboard");
  });

  // (i) Recursive-but-still-relative path — PASSES.
  // The helper validates SHAPE, not query-string content. The path
  // `/login?from=https://evil.com` is a valid relative path; the
  // SECOND-order open-redirect (the `signIn` call that re-fires on
  // /login) is fixed because the helper runs there too. AC-3 (i) in
  // slice 086 documents this intentionally.
  test("passes through /login?from=https://evil.com", () => {
    expect(safeRedirectTarget("/login?from=https://evil.com")).toBe(
      "/login?from=https://evil.com",
    );
  });
});
