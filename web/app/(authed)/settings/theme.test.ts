// Slice 103 -- vitest unit coverage for the /settings theme persistence
// helpers.
//
// Pure-logic tests. The theme picker writes the user's choice to
// localStorage and reads it back on next visit. The application of the
// theme to document.documentElement is a side-effect performed by the
// page component (covered indirectly by the Playwright spec); the
// persistence contract itself is what we test here in isolation.
//
// All test fixtures use neutral identifiers -- NO vendor token prefixes
// (per slice 103 anti-criterion P0-A5 and slice 069 hardening).

import { afterEach, beforeEach, describe, expect, test } from "vitest";

import {
  DEFAULT_THEME,
  parseTheme,
  readTheme,
  THEME_STORAGE_KEY,
  type Theme,
  writeTheme,
} from "./theme";

// Minimal in-memory localStorage shim. The vitest env is `node`, which
// has no `window.localStorage`. The helpers are written to accept a
// Storage-like object so the tests can pass a deterministic instance
// without polluting globals.
class MemStorage {
  private map = new Map<string, string>();
  getItem(k: string): string | null {
    return this.map.has(k) ? this.map.get(k)! : null;
  }
  setItem(k: string, v: string): void {
    this.map.set(k, v);
  }
  removeItem(k: string): void {
    this.map.delete(k);
  }
  clear(): void {
    this.map.clear();
  }
}

let store: MemStorage;
beforeEach(() => {
  store = new MemStorage();
});
afterEach(() => {
  store.clear();
});

describe("parseTheme", () => {
  test("accepts the three canonical values", () => {
    expect(parseTheme("light")).toBe<Theme>("light");
    expect(parseTheme("dark")).toBe<Theme>("dark");
    expect(parseTheme("system")).toBe<Theme>("system");
  });

  test("returns DEFAULT_THEME on unrecognized input", () => {
    expect(parseTheme("solarized")).toBe<Theme>(DEFAULT_THEME);
    expect(parseTheme("")).toBe<Theme>(DEFAULT_THEME);
    expect(parseTheme(null)).toBe<Theme>(DEFAULT_THEME);
    expect(parseTheme(undefined)).toBe<Theme>(DEFAULT_THEME);
  });

  test("DEFAULT_THEME is system", () => {
    expect(DEFAULT_THEME).toBe<Theme>("system");
  });
});

describe("readTheme / writeTheme round-trip", () => {
  test("writeTheme then readTheme returns the same value", () => {
    writeTheme(store, "dark");
    expect(readTheme(store)).toBe<Theme>("dark");
  });

  test("readTheme returns DEFAULT_THEME when nothing is stored", () => {
    expect(readTheme(store)).toBe<Theme>(DEFAULT_THEME);
  });

  test("readTheme defaults when stored value is garbage", () => {
    store.setItem(THEME_STORAGE_KEY, "neon");
    expect(readTheme(store)).toBe<Theme>(DEFAULT_THEME);
  });

  test("writeTheme is idempotent", () => {
    writeTheme(store, "light");
    writeTheme(store, "light");
    writeTheme(store, "light");
    expect(readTheme(store)).toBe<Theme>("light");
  });

  test("writeTheme overwrites prior value", () => {
    writeTheme(store, "dark");
    writeTheme(store, "light");
    expect(readTheme(store)).toBe<Theme>("light");
  });

  test("THEME_STORAGE_KEY is namespaced to the app", () => {
    // The key MUST start with a vendor-neutral app prefix so it does
    // not collide with other browser content. We pin the exact key so
    // a refactor that changes it surfaces in CI as a test failure --
    // existing users would otherwise silently lose their preference.
    expect(THEME_STORAGE_KEY).toBe("security-atlas.settings.theme");
  });
});

describe("readTheme / writeTheme survive page reload", () => {
  test("a second readTheme on the same store sees the prior write", () => {
    // Simulates: user writes a theme, navigates away, returns. The
    // storage instance is the same (the browser's), so the prior write
    // is visible.
    writeTheme(store, "dark");
    // First read -- same tick.
    expect(readTheme(store)).toBe<Theme>("dark");
    // Second read -- simulates the next page load after a reload. No
    // new write in between; the value MUST still be there.
    expect(readTheme(store)).toBe<Theme>("dark");
  });
});
