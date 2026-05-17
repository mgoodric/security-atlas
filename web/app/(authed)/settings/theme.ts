// Slice 103 -- theme persistence helpers for /settings.
//
// The theme picker writes one of three canonical values to localStorage
// and reads it back on next visit. The application of the theme to the
// document (setting a `data-theme` attribute on <html>) is performed by
// the page component on mount; this module owns ONLY the persistence
// contract.
//
// localStorage is the v1 fallback per slice 103 AC-2 narrative -- no
// server-side theme persistence endpoint exists. The spillover slice
// covers adding `PATCH /v1/me` for cross-device theme sync.

export type Theme = "light" | "dark" | "system";

// The default value when nothing is stored or the stored value is
// unrecognized. `system` defers to the user's OS-level preference and
// is the safest default for first-time visitors.
export const DEFAULT_THEME: Theme = "system";

// Pinned storage key. Changing this would silently log out every
// user's prior theme choice -- the corresponding test fails on
// rename so the cost is visible.
export const THEME_STORAGE_KEY = "security-atlas.settings.theme";

const VALID: ReadonlySet<Theme> = new Set<Theme>(["light", "dark", "system"]);

// Storage-shaped subset; matches the relevant subset of the DOM Storage
// interface. The page passes `window.localStorage`; tests pass an
// in-memory shim.
export interface ThemeStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

// Normalize an unknown value to a Theme. Returns DEFAULT_THEME on
// anything that isn't one of the three canonical values.
export function parseTheme(value: unknown): Theme {
  if (typeof value !== "string") return DEFAULT_THEME;
  if (VALID.has(value as Theme)) return value as Theme;
  return DEFAULT_THEME;
}

// Read the persisted theme from a Storage-like object. Returns
// DEFAULT_THEME when nothing is stored or the stored value is invalid.
export function readTheme(store: ThemeStore): Theme {
  return parseTheme(store.getItem(THEME_STORAGE_KEY));
}

// Write the theme. Idempotent. Throws if the underlying Storage
// implementation throws (e.g. quota exceeded) -- the caller decides
// how to surface that.
export function writeTheme(store: ThemeStore, theme: Theme): void {
  store.setItem(THEME_STORAGE_KEY, theme);
}
