// Slice 176 -- pure picker logic for the theme-aware logo variant.
//
// The component (`web/components/shell/theme-aware-logo.tsx`) wires this
// to React state + DOM observers; this module owns only the decision
// function so it is unit-testable in vitest's `node` environment (no
// jsdom, no @testing-library/react -- per slice 069 + slice 103's
// pure-logic-tests convention).
//
// Decision table (see slice 176 ACs 2-4):
//
//   app theme   | prefers-color-scheme | served variant
//   ----------- | -------------------- | ---------------
//   "light"     | (ignored)            | /logo-light.svg
//   "dark"      | (ignored)            | /logo-dark.svg
//   "system"    | light                | /logo-light.svg
//   "system"    | dark                 | /logo-dark.svg
//
// Slice 170's AppearanceSelector writes one of the three Theme values to
// `data-theme` on <html> (see web/app/(authed)/settings/page.tsx); when
// the attribute is absent (first-visit) or holds an unrecognized value,
// `parseTheme()` from ./theme normalizes to DEFAULT_THEME = "system", so
// the prefers-color-scheme fallback applies. That is the contract: this
// module never invents a third state.
//
// Asset paths are pinned constants so a typo at a call site cannot
// silently break the mapping; tests assert exact equality.

import type { Theme } from "@/app/(authed)/settings/theme";

export const LOGO_LIGHT_SRC = "/logo-light.svg" as const;
export const LOGO_DARK_SRC = "/logo-dark.svg" as const;

export type LogoSrc = typeof LOGO_LIGHT_SRC | typeof LOGO_DARK_SRC;

// Resolve the logo `src` for a given app-theme state + OS prefers-dark
// signal. Pure function: no DOM access, no side effects. The caller
// (the React component) is responsible for reading `data-theme` from
// <html> and `matchMedia("(prefers-color-scheme: dark)").matches`.
//
// `prefersDark` is the boolean result of `matchMedia`. When the
// component renders during SSR (no window), the caller passes `false`
// so the function deterministically picks the light variant -- which
// matches the existing slice 075 `<picture>` element's fallback `<img>`
// src and keeps the hydration mismatch surface zero.
export function resolveLogoSrc(appTheme: Theme, prefersDark: boolean): LogoSrc {
  if (appTheme === "light") return LOGO_LIGHT_SRC;
  if (appTheme === "dark") return LOGO_DARK_SRC;
  // appTheme === "system": defer to OS preference.
  return prefersDark ? LOGO_DARK_SRC : LOGO_LIGHT_SRC;
}
