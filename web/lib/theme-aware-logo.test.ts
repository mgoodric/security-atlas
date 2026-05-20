// Slice 176 -- vitest unit coverage for the logo-variant picker.
//
// Covers AC-8: each of the four (app-theme x prefers-color-scheme)
// combinations resolves to the expected /logo-{light,dark}.svg path.
// The component layer (web/components/shell/theme-aware-logo.tsx) is
// a thin wrapper that reads <html data-theme> + matchMedia and calls
// resolveLogoSrc; testing the pure function gives full coverage of
// the decision table without a jsdom harness (slice 069 P0-A3 + slice
// 103 pattern -- pure-logic tests only in vitest, DOM behavior covered
// in the Playwright e2e under web/e2e/logo-render.spec.ts).
//
// Neutral test tokens / no vendor-prefixed strings (slice 176 P0-A10).

import { describe, expect, test } from "vitest";

import {
  LOGO_DARK_SRC,
  LOGO_LIGHT_SRC,
  resolveLogoSrc,
} from "./theme-aware-logo";

describe("resolveLogoSrc -- explicit app theme overrides OS preference", () => {
  test("app=light + OS=light -> light variant", () => {
    expect(resolveLogoSrc("light", false)).toBe(LOGO_LIGHT_SRC);
  });

  test("app=light + OS=dark -> light variant (Bug A regression)", () => {
    // This is the bug the slice exists to fix: operator on OS=dark with
    // an explicit app theme of "light" was being served logo-dark.svg
    // (near-white ink) against a light app background. The picker MUST
    // honor the explicit choice and ignore the OS signal.
    expect(resolveLogoSrc("light", true)).toBe(LOGO_LIGHT_SRC);
  });

  test("app=dark + OS=light -> dark variant", () => {
    expect(resolveLogoSrc("dark", false)).toBe(LOGO_DARK_SRC);
  });

  test("app=dark + OS=dark -> dark variant", () => {
    expect(resolveLogoSrc("dark", true)).toBe(LOGO_DARK_SRC);
  });
});

describe("resolveLogoSrc -- system app theme defers to OS preference", () => {
  test("app=system + OS=light -> light variant", () => {
    expect(resolveLogoSrc("system", false)).toBe(LOGO_LIGHT_SRC);
  });

  test("app=system + OS=dark -> dark variant", () => {
    expect(resolveLogoSrc("system", true)).toBe(LOGO_DARK_SRC);
  });
});

describe("resolveLogoSrc -- pinned asset paths", () => {
  test("LOGO_LIGHT_SRC points at the slice-167 light SVG path", () => {
    expect(LOGO_LIGHT_SRC).toBe("/logo-light.svg");
  });

  test("LOGO_DARK_SRC points at the slice-167 dark SVG path", () => {
    expect(LOGO_DARK_SRC).toBe("/logo-dark.svg");
  });
});
