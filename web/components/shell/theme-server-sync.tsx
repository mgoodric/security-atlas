"use client";

// #2142 -- the server copy of the theme wins over the browser cache.
//
// The theme choice is stored per user on the users row (PATCH /v1/me).
// localStorage stays the first-paint cache: the inline script in
// `web/app/layout.tsx` reads it before React exists, so the page paints in
// the cached theme with no flash. The top bar already reads /v1/me on the
// server for the avatar and hands the stored theme here; when it differs
// from the cache (the user chose on another device, or the cache was
// cleared) this re-applies it and refreshes the cache, so the next first
// paint is right too.
//
// `theme` is null for a credential bearer with no users row: there is no
// server copy, and the browser cache stays authoritative.

import { useEffect } from "react";

import {
  readTheme,
  writeTheme,
  type Theme,
} from "@/app/(authed)/settings/theme";
import { applyThemeToDocument } from "@/lib/theme-class";

export function ThemeServerSync({ theme }: { theme: Theme | null }) {
  useEffect(() => {
    if (theme === null) return;
    try {
      if (readTheme(window.localStorage) === theme) return;
      writeTheme(window.localStorage, theme);
    } catch {
      // Storage blocked: still apply the stored theme to this page.
    }
    applyThemeToDocument(theme);
  }, [theme]);

  return null;
}
