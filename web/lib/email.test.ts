// Slice 679 (ATLAS-032) — vitest coverage for the owner-email predicate.
//
// Neutral fixtures only — NO real-vendor token prefixes, only
// `@demo.example` / `@example.com` addresses (slice 069 / GitGuardian
// hardening).

import { describe, expect, test } from "vitest";

import { isEmail, VENDOR_OWNER_EMAIL_ERROR } from "./email";

describe("isEmail", () => {
  test("accepts a plain address", () => {
    expect(isEmail("alice@demo.example")).toBe(true);
  });

  test("accepts dotted-and-tagged local part with subdomain", () => {
    expect(isEmail("a.b+tag@sub.domain.co")).toBe(true);
  });

  test("trims surrounding whitespace before testing", () => {
    expect(isEmail("  alice@demo.example  ")).toBe(true);
  });

  // The ATLAS-032 regression: a role string is NOT an email.
  test("rejects a role string (the seeded bug)", () => {
    expect(isEmail("Head of Security")).toBe(false);
  });

  test("rejects empty / nullish", () => {
    expect(isEmail("")).toBe(false);
    expect(isEmail(null)).toBe(false);
    expect(isEmail(undefined)).toBe(false);
  });

  test("rejects a bare username with no domain", () => {
    expect(isEmail("alice")).toBe(false);
  });

  test("rejects a domain with no dot", () => {
    expect(isEmail("alice@localhost")).toBe(false);
  });

  test("rejects a trailing-@ value", () => {
    expect(isEmail("alice@")).toBe(false);
  });

  test("rejects an address with embedded whitespace", () => {
    expect(isEmail("ali ce@demo.example")).toBe(false);
  });

  test("rejects multiple @ signs", () => {
    expect(isEmail("a@b@demo.example")).toBe(false);
  });
});

describe("VENDOR_OWNER_EMAIL_ERROR", () => {
  test("is a non-empty user-facing string", () => {
    expect(VENDOR_OWNER_EMAIL_ERROR.length).toBeGreaterThan(0);
  });
});
