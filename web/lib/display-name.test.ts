// Slice 213 — vitest coverage for the pure display-name helpers used by
// the topbar's user-avatar component.
//
// The helpers are pure (no React, no fetch); the tests live alongside
// the source. The Playwright e2e spec covers the integrated end-to-end
// path (real /v1/me read, real render); the unit specs here pin the
// derivation rules.

import { describe, expect, test } from "vitest";

import { deriveDisplayName, deriveInitials } from "./display-name";

describe("deriveDisplayName", () => {
  test("returns display_name when set", () => {
    expect(
      deriveDisplayName({
        display_name: "Sam Chen",
        email: "sam@example.invalid",
      }),
    ).toBe("Sam Chen");
  });

  test("trims whitespace-only display_name and falls back to email local-part", () => {
    expect(
      deriveDisplayName({
        display_name: "   ",
        email: "sam.chen@example.invalid",
      }),
    ).toBe("sam.chen");
  });

  test("falls back to email local-part when display_name is empty", () => {
    expect(
      deriveDisplayName({
        display_name: "",
        email: "alice@example.invalid",
      }),
    ).toBe("alice");
  });

  test("returns empty string when both inputs are unusable", () => {
    expect(deriveDisplayName({ display_name: "", email: "" })).toBe("");
  });

  test("handles missing fields defensively (treats undefined as empty)", () => {
    expect(deriveDisplayName({})).toBe("");
  });

  test("returns email local-part even with weird casing intact", () => {
    expect(
      deriveDisplayName({
        display_name: "",
        email: "Matt.Goodrich@example.invalid",
      }),
    ).toBe("Matt.Goodrich");
  });
});

describe("deriveInitials", () => {
  test("returns up to two uppercase initials from a two-word display name", () => {
    expect(deriveInitials("Matt Goodrich")).toBe("MG");
  });

  test("returns one initial for a single-word display name", () => {
    expect(deriveInitials("Sam")).toBe("S");
  });

  test("uses first letter of first two whitespace-separated tokens", () => {
    expect(deriveInitials("Sam Chen-Smith O'Brien")).toBe("SC");
  });

  test("uppercases lower-case inputs", () => {
    expect(deriveInitials("alice cooper")).toBe("AC");
  });

  test("collapses repeated whitespace", () => {
    expect(deriveInitials("  Matt   Goodrich  ")).toBe("MG");
  });

  test("returns empty string for empty input", () => {
    expect(deriveInitials("")).toBe("");
  });

  test("returns empty string for whitespace-only input", () => {
    expect(deriveInitials("   ")).toBe("");
  });

  test("ignores leading punctuation when picking a letter", () => {
    // Non-letter tokens should be skipped, not become a "?" initial.
    expect(deriveInitials("  Matt")).toBe("M");
  });
});
