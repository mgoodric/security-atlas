// Slice 146 — vitest coverage for the secure-cookie attribute helper.
//
// The function under test (`shouldUseSecureCookie`) decides whether the
// `signIn` server action should mark the session cookie with the
// `Secure` attribute when calling `cookies().set(...)`.
//
// Why this exists: before slice 146 the call site used a blunt
// `secure: process.env.NODE_ENV === "production"` check. That set
// `Secure` on every production-build deployment — including the many
// self-hosted operators (Unraid, docker-compose without TLS, local
// production-build smoke runs) that serve the app over plain HTTP.
// Browsers refuse to send `Secure` cookies over HTTP, so the cookie
// never came back to the BFF, `web/proxy.ts` saw no `SESSION_COOKIE`,
// redirected `/api/dashboard/**` (and every other BFF path) to
// `/login`, and the browser fetch parsed the resulting HTML as JSON —
// the "Unexpected token '<'" symptom captured in
// `docs/audit-log/132-readme-refresh-decisions.md` D5.
//
// The contract the four cases enumerate:
//   1. `X-Forwarded-Proto: https` → trust the reverse proxy → secure=true
//   2. `Forwarded: proto=https` (RFC 7239) → secure=true
//   3. `X-Forwarded-Proto: http` → reverse proxy is plain-HTTP → secure=false
//   4. No proto signals → default-INSECURE (the self-host plain-HTTP
//      case is the dominant deployment shape, so this default matches
//      "the cookie should actually round-trip" over "the cookie should
//      refuse to round-trip"). HTTPS deployments behind any reverse
//      proxy emit `X-Forwarded-Proto: https` and hit case 1.

import { describe, expect, test } from "vitest";

import { shouldUseSecureCookie } from "./secure-cookie";

describe("shouldUseSecureCookie", () => {
  test("returns true when X-Forwarded-Proto is https", () => {
    const headers = new Headers({ "x-forwarded-proto": "https" });
    expect(shouldUseSecureCookie(headers)).toBe(true);
  });

  test("returns true when X-Forwarded-Proto is HTTPS (case-insensitive)", () => {
    const headers = new Headers({ "x-forwarded-proto": "HTTPS" });
    expect(shouldUseSecureCookie(headers)).toBe(true);
  });

  test("returns false when X-Forwarded-Proto is http", () => {
    const headers = new Headers({ "x-forwarded-proto": "http" });
    expect(shouldUseSecureCookie(headers)).toBe(false);
  });

  test("returns true when RFC 7239 Forwarded header has proto=https", () => {
    const headers = new Headers({
      forwarded: "for=192.0.2.1;proto=https;by=203.0.113.43",
    });
    expect(shouldUseSecureCookie(headers)).toBe(true);
  });

  test("returns false when RFC 7239 Forwarded header has proto=http", () => {
    const headers = new Headers({ forwarded: "for=192.0.2.1;proto=http" });
    expect(shouldUseSecureCookie(headers)).toBe(false);
  });

  test("returns false when no proto signal present (self-host plain HTTP default)", () => {
    const headers = new Headers();
    expect(shouldUseSecureCookie(headers)).toBe(false);
  });

  test("X-Forwarded-Proto takes precedence over Forwarded when both present", () => {
    // Real-world: nginx adds X-Forwarded-Proto; an upstream CDN added
    // Forwarded. The immediately-adjacent proxy is the authoritative
    // signal for the request scheme reaching us, and that is the one
    // setting X-Forwarded-Proto.
    const headers = new Headers({
      "x-forwarded-proto": "https",
      forwarded: "for=192.0.2.1;proto=http",
    });
    expect(shouldUseSecureCookie(headers)).toBe(true);
  });

  test("returns false for malformed X-Forwarded-Proto value", () => {
    // Defense-in-depth: anything that isn't literal "https" is treated
    // as not-secure. An attacker who could inject this header has bigger
    // problems, but the helper still fails closed for the cookie-Secure
    // attribute decision (which is the safer default for the regression
    // this slice fixes: not-Secure means the cookie WILL round-trip on
    // the wrong transport, but only on a server an attacker already
    // controls. Secure-and-not-sent breaks every self-host operator).
    const headers = new Headers({ "x-forwarded-proto": "garbage" });
    expect(shouldUseSecureCookie(headers)).toBe(false);
  });
});
