// Slice 203 -- vitest coverage for applyThemeClass + isDarkActive.
//
// These cover AC-6 (a, b, c) at the helper level. The runtime
// integration (the actual `<html>` element receiving the class on a
// click) is covered by the Playwright spec extension at
// web/e2e/settings.spec.ts ("AC-12: selecting dark applies CSS theming").
//
// The vitest env is `node` (slice 069 P0-A3 -- no jsdom, no
// @testing-library/react). The helper accepts an element-shaped object
// so we can test against a minimal stub without a DOM. NO vendor-
// prefixed test tokens.

import { afterEach, beforeEach, describe, expect, test } from "vitest";

import {
  applyPersistedThemeClass,
  applyThemeClass,
  DARK_CLASS,
  isDarkActive,
  type ClassListTarget,
} from "./theme-class";
import { THEME_STORAGE_KEY } from "@/app/(authed)/settings/theme";

// Minimal DOMTokenList-shaped stub. Tracks set-membership in a JS Set so
// assertions can introspect via `.contains()` or via the `classes` Set
// directly. Mirrors the slice 103 MemStorage pattern (deterministic,
// no jsdom).
function makeTarget(initial: string[] = []): ClassListTarget & {
  readonly classes: Set<string>;
} {
  const classes = new Set<string>(initial);
  return {
    classes,
    classList: {
      add(token: string) {
        classes.add(token);
      },
      remove(token: string) {
        classes.delete(token);
      },
      contains(token: string) {
        return classes.has(token);
      },
    },
  };
}

class MemStorage {
  private map = new Map<string, string>();
  getItem(k: string): string | null {
    return this.map.has(k) ? this.map.get(k)! : null;
  }
  setItem(k: string, v: string): void {
    this.map.set(k, v);
  }
  clear(): void {
    this.map.clear();
  }
}

describe("isDarkActive -- decision table", () => {
  test('"light" + OS-light -> false', () => {
    expect(isDarkActive("light", false)).toBe(false);
  });

  test('"light" + OS-dark -> false (explicit choice overrides OS)', () => {
    // Mirrors the slice 176 Bug A scenario at the class-toggle layer.
    // An operator who explicitly picked "light" MUST stay in light mode
    // regardless of their OS preference.
    expect(isDarkActive("light", true)).toBe(false);
  });

  test('"dark" + OS-light -> true', () => {
    expect(isDarkActive("dark", false)).toBe(true);
  });

  test('"dark" + OS-dark -> true', () => {
    expect(isDarkActive("dark", true)).toBe(true);
  });

  test('"system" + OS-light -> false', () => {
    expect(isDarkActive("system", false)).toBe(false);
  });

  test('"system" + OS-dark -> true', () => {
    expect(isDarkActive("system", true)).toBe(true);
  });
});

describe("applyThemeClass -- DOM mutation contract", () => {
  test('choose("dark") adds the dark class', () => {
    // AC-6 (a): the picker click path.
    const target = makeTarget();
    applyThemeClass(target, "dark", false);
    expect(target.classes.has(DARK_CLASS)).toBe(true);
  });

  test('choose("light") removes the dark class', () => {
    // AC-6 (b): the reverse path. Starts from a "dark"-already state.
    const target = makeTarget([DARK_CLASS]);
    applyThemeClass(target, "light", false);
    expect(target.classes.has(DARK_CLASS)).toBe(false);
  });

  test('choose("system") + OS-dark adds the dark class', () => {
    // AC-6 (c) -- positive arm.
    const target = makeTarget();
    applyThemeClass(target, "system", true);
    expect(target.classes.has(DARK_CLASS)).toBe(true);
  });

  test('choose("system") + OS-light removes the dark class', () => {
    // AC-6 (c) -- negative arm. Starts from a "dark"-already state to
    // prove the remove path is exercised, not just the bare initial.
    const target = makeTarget([DARK_CLASS]);
    applyThemeClass(target, "system", false);
    expect(target.classes.has(DARK_CLASS)).toBe(false);
  });

  test("idempotent: calling twice with the same input is a no-op", () => {
    const target = makeTarget();
    applyThemeClass(target, "dark", false);
    applyThemeClass(target, "dark", false);
    expect(target.classes.has(DARK_CLASS)).toBe(true);
    // No assertions on call counts -- the Set just rejects the duplicate.
  });

  test("does NOT touch unrelated classes", () => {
    // The page root carries Geist font variables + `antialiased` set by
    // `web/app/layout.tsx`. The slice MUST NOT clobber them when
    // toggling `dark`.
    const target = makeTarget([
      "__className_a1b2c3",
      "__className_d4e5f6",
      "antialiased",
    ]);
    applyThemeClass(target, "dark", false);
    expect(target.classes.has(DARK_CLASS)).toBe(true);
    expect(target.classes.has("antialiased")).toBe(true);
    expect(target.classes.has("__className_a1b2c3")).toBe(true);
    expect(target.classes.has("__className_d4e5f6")).toBe(true);

    applyThemeClass(target, "light", false);
    expect(target.classes.has(DARK_CLASS)).toBe(false);
    expect(target.classes.has("antialiased")).toBe(true);
    expect(target.classes.has("__className_a1b2c3")).toBe(true);
    expect(target.classes.has("__className_d4e5f6")).toBe(true);
  });
});

describe("applyPersistedThemeClass -- early-paint storage read", () => {
  // AC-6 (d): the page-load init path. The inline script in
  // `web/app/layout.tsx` reads localStorage synchronously and applies
  // the class. The runtime equivalent is `applyPersistedThemeClass`,
  // which `<ThemeClassSync>` calls on mount to converge on the same
  // outcome the inline script already produced. The two MUST agree;
  // these tests pin the runtime side.
  let store: MemStorage;
  beforeEach(() => {
    store = new MemStorage();
  });
  afterEach(() => {
    store.clear();
  });

  test('persisted "dark" -> class is added', () => {
    store.setItem(THEME_STORAGE_KEY, "dark");
    const target = makeTarget();
    const resolved = applyPersistedThemeClass({
      target,
      store,
      prefersDark: false,
      storageKey: THEME_STORAGE_KEY,
    });
    expect(resolved).toBe("dark");
    expect(target.classes.has(DARK_CLASS)).toBe(true);
  });

  test('persisted "light" -> class is removed even if stub started dark', () => {
    store.setItem(THEME_STORAGE_KEY, "light");
    const target = makeTarget([DARK_CLASS]);
    const resolved = applyPersistedThemeClass({
      target,
      store,
      prefersDark: true,
      storageKey: THEME_STORAGE_KEY,
    });
    expect(resolved).toBe("light");
    expect(target.classes.has(DARK_CLASS)).toBe(false);
  });

  test('empty store -> resolves to "system" and follows OS preference', () => {
    // First-time visitor: parseTheme normalizes the missing value to
    // DEFAULT_THEME ("system"). The OS preference then drives the
    // class.
    const target = makeTarget();
    const resolved = applyPersistedThemeClass({
      target,
      store,
      prefersDark: true,
      storageKey: THEME_STORAGE_KEY,
    });
    expect(resolved).toBe("system");
    expect(target.classes.has(DARK_CLASS)).toBe(true);
  });

  test("corrupted store -> falls back to default theme (no exception)", () => {
    store.setItem(THEME_STORAGE_KEY, "midnight");
    const target = makeTarget();
    const resolved = applyPersistedThemeClass({
      target,
      store,
      prefersDark: false,
      storageKey: THEME_STORAGE_KEY,
    });
    // DEFAULT_THEME is "system"; OS=light -> no dark class.
    expect(resolved).toBe("system");
    expect(target.classes.has(DARK_CLASS)).toBe(false);
  });

  test("DARK_CLASS is the token Tailwind reads in globals.css", () => {
    // Pin the constant. The `globals.css:5` `@custom-variant dark
    // (&:is(.dark *))` selector keys off this exact token. Renaming the
    // constant without updating the CSS would silently break the wire.
    expect(DARK_CLASS).toBe("dark");
  });
});
