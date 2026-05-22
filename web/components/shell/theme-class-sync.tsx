"use client";

// Slice 203 -- mount-side reactivity for the dark-mode class.
//
// The inline early-paint script in `web/app/layout.tsx` runs in the
// browser's pre-React phase and sets the `dark` class on <html> before
// first paint (AC-3). Once React hydrates, the script can no longer
// react to changes. This component owns the runtime side of the same
// contract:
//
//   1. On mount, RE-READ the persisted theme and OS preference and
//      apply. This is technically redundant with the inline script
//      (the script already converged on the same outcome), but it
//      ensures that if a future refactor breaks the inline script,
//      the class is still set after hydration. The cost is a single
//      no-op classList write per page load.
//
//   2. Listen for `prefers-color-scheme` MediaQueryList changes (AC-4).
//      When an operator with `theme=system` toggles their OS dark/light
//      setting, the page re-themes WITHOUT a reload. This mirrors the
//      slice 176 `ThemeAwareLogo` reactivity discipline so both the
//      logo and the page-class stay in sync with the same OS signal.
//
//   3. Listen for `<html data-theme>` attribute changes via a
//      MutationObserver. The /settings picker writes the attribute (and
//      the class) directly via `choose()`, so the observer is mostly a
//      defense-in-depth path -- if a future feature writes only the
//      attribute (mirroring slice 170's original contract), the class
//      still converges. Pattern parallels the slice 176 logo observer.
//
// P0-A6: the inline script MUST stay in place (AC-3); this component
// is not a replacement. It is the runtime tail of the same contract.
// Removing the inline script reintroduces the one-frame flash this
// slice exists to fix.
//
// SSR safety: this component renders no DOM. It mounts under
// <Providers> in the root layout so its useEffect runs once per page
// load, after hydration. The `null` render means no hydration mismatch
// surface.

import { useEffect } from "react";

import {
  parseTheme,
  THEME_STORAGE_KEY,
} from "@/app/(authed)/settings/theme";
import { applyThemeClass } from "@/lib/theme-class";

export function ThemeClassSync() {
  useEffect(() => {
    // Read both signals at mount and converge the class. This is a
    // no-op if the inline script already wrote the correct class, but
    // it makes the runtime contract self-healing.
    const html = document.documentElement;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");

    const readTheme = () => {
      // Prefer the `data-theme` attribute (the slice 170 contract) so a
      // tab that observes a SECOND tab's /settings selection (via
      // BroadcastChannel or storage-events; not implemented yet but on
      // the v3 roadmap) sees the same source-of-truth. Falls back to
      // localStorage when the attribute is absent.
      const attr = html.getAttribute("data-theme");
      if (attr) return parseTheme(attr);
      return parseTheme(window.localStorage.getItem(THEME_STORAGE_KEY));
    };

    const sync = () => {
      applyThemeClass(html, readTheme(), mq.matches);
    };

    sync();

    const onPrefChange = () => sync();
    mq.addEventListener("change", onPrefChange);

    const observer = new MutationObserver(() => sync());
    observer.observe(html, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });

    return () => {
      mq.removeEventListener("change", onPrefChange);
      observer.disconnect();
    };
  }, []);

  return null;
}
