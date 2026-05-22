// Slice 203 -- Apply the persisted theme as a Tailwind-readable class on
// the document root.
//
// Context: slice 170 wired the /settings picker to write the operator's
// choice to localStorage and to <html data-theme="...">. Slice 176 wired
// the logo to read `data-theme` and swap variants. Neither slice wrote
// the `class="dark"` selector that the Tailwind v4 dark variant configured
// in `web/app/globals.css:5` actually keys off
// (`@custom-variant dark (&:is(.dark *))`). The result: selecting "Dark"
// swapped the logo (light-ink, for dark backgrounds) but left the page
// styles in light mode -> light-ink logo on white page = invisible.
//
// This module owns the small DOM mutation: read a Theme + an OS-pref
// boolean, and toggle the `dark` class on a target Element. It is a pure
// helper -- no localStorage, no global lookups -- so vitest can exercise
// every branch against an injected element-shaped object (the vitest env
// is `node`; no jsdom is configured per slice 069 P0-A3).
//
// The same logic is INLINED as a string into a `<script>` block in
// `web/app/layout.tsx` so the class is set BEFORE first paint -- a
// useEffect-only implementation would flash light mode on every page
// load for users whose persisted preference is dark. See slice 203
// decisions log D1 for the script text.
//
// P0-A1: this module does NOT change the source-of-truth attribute from
// `data-theme` to `class="dark"`. Slice 176's `ThemeAwareLogo` and any
// future consumer keep reading `data-theme`; the class is in addition.
// The two are written together in `choose()` (slice 170 contract).
//
// P0-A4: this module does NOT touch `globals.css` token values. The
// `.dark { --background: ...; ... }` block already exists (lines 86+);
// this slice only ACTIVATES it by writing the matching class.

import { parseTheme, type Theme } from "@/app/(authed)/settings/theme";

export const DARK_CLASS = "dark" as const;

// Element-shaped subset for testability. The runtime passes
// `document.documentElement`; tests pass a minimal `classList`-bearing
// stub. We deliberately type only the two methods we touch so a future
// refactor that reaches for `.setAttribute` or `.style` has to widen
// the surface explicitly.
export interface ClassListTarget {
  classList: {
    add(token: string): void;
    remove(token: string): void;
    contains(token: string): boolean;
  };
}

// Resolve whether the dark variant should be active for a (theme, OS)
// pair. Pure function -- mirrors the decision table in
// `lib/theme-aware-logo.ts:resolveLogoSrc` but returns a boolean instead
// of a logo path. Both modules MUST stay in sync (any divergence
// re-introduces the bug the slice exists to fix).
//
//   theme       | prefersDark | result
//   ----------- | ----------- | ------
//   "light"     | (ignored)   | false
//   "dark"      | (ignored)   | true
//   "system"    | false       | false
//   "system"    | true        | true
export function isDarkActive(theme: Theme, prefersDark: boolean): boolean {
  if (theme === "light") return false;
  if (theme === "dark") return true;
  // theme === "system" -> defer to OS preference.
  return prefersDark;
}

// Apply (or remove) the dark class on the target element based on the
// (theme, OS-pref) pair. Idempotent: calling twice with the same input
// is a no-op.
//
// Callers:
//   * `choose()` in `web/app/(authed)/settings/page.tsx` -- runs on every
//     theme picker click, immediately after `writeTheme()`.
//   * `<ThemeClassSync>` in `web/app/providers.tsx` -- runs on mount and
//     on every `prefers-color-scheme` MediaQueryList change.
//   * Inline early-paint script in `web/app/layout.tsx` -- the script
//     reproduces this logic LITERALLY (no import possible from a
//     `dangerouslySetInnerHTML` string), so a divergence between the
//     two would cause a one-frame flash. See decisions log D1 for the
//     exact script text the two must stay in sync with.
export function applyThemeClass(
  target: ClassListTarget,
  theme: Theme,
  prefersDark: boolean,
): void {
  if (isDarkActive(theme, prefersDark)) {
    target.classList.add(DARK_CLASS);
  } else {
    target.classList.remove(DARK_CLASS);
  }
}

// Convenience wrapper for the runtime path: read the persisted theme
// from a Storage-like object, query matchMedia, and apply. Returns the
// resolved Theme so callers (the React mount-effect) can sync local
// state without re-reading storage themselves. Not used by the inline
// script (the script does its own storage read for synchronous-paint
// reasons; see D1).
export function applyPersistedThemeClass(args: {
  target: ClassListTarget;
  store: { getItem(key: string): string | null };
  prefersDark: boolean;
  storageKey: string;
}): Theme {
  const theme = parseTheme(args.store.getItem(args.storageKey));
  applyThemeClass(args.target, theme, args.prefersDark);
  return theme;
}
