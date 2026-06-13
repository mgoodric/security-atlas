"use client";

import { useEffect, useState } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

import { applyThemeClass } from "@/lib/theme-class";

import { DEFAULT_THEME, readTheme, Theme, writeTheme } from "../theme";

// --- Section 2: Appearance ------------------------------------------------

// Slice 154: each theme option carries a swatch preview class (the
// mockup shows a 48-px-tall card-shaped preview above the label so the
// user picks visually instead of reading three descriptions). The
// `swatch` class is a Tailwind utility composition — no new components
// added (Article VIII Anti-Abstraction).
const THEMES: {
  value: Theme;
  label: string;
  description: string;
  swatch: string;
}[] = [
  {
    value: "light",
    label: "Light",
    description: "Bright background",
    swatch: "bg-white border border-border",
  },
  {
    value: "dark",
    label: "Dark",
    description: "Low-light reading",
    swatch: "bg-slate-900 border border-slate-700",
  },
  {
    value: "system",
    label: "System",
    description: "Follow OS preference",
    swatch: "bg-gradient-to-br from-white to-slate-900 border border-border",
  },
];

export function AppearanceSection() {
  // The theme starts at DEFAULT_THEME during SSR (no localStorage on the
  // server). On mount, the AppearanceSelector child re-reads from
  // localStorage with a lazy initializer to avoid a hydration mismatch
  // while sidestepping the react-hooks/set-state-in-effect rule.
  return (
    <Card id="appearance" data-testid="settings-section-appearance">
      <CardHeader>
        <CardTitle>Appearance</CardTitle>
        <CardDescription>
          Theme preference is stored in your browser (no cross-device sync in
          this release).
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <AppearanceSelector />
        {/*
         * Slice 203 — the slice-170 "Dark-mode stylesheet pending" banner is
         * retired. The class wire is live: selecting Dark or System (with
         * OS=dark) now activates the `.dark { ... }` token block in
         * globals.css; data-theme stays written for ThemeAwareLogo
         * compatibility.
         */}
      </CardContent>
    </Card>
  );
}

function AppearanceSelector() {
  // Slice 170 D1 (Pattern A: useEffect post-mount sync) — the prior
  // implementation used `useState` with an SSR-guarded lazy initializer.
  // That initializer runs exactly once per server-or-client render PASS,
  // and React reuses the server-rendered state on hydration: the client
  // never re-ran the initializer, so `localStorage` was never consulted
  // on a fresh page load. Result: the picker always booted to
  // `DEFAULT_THEME` regardless of the user's persisted choice. The fix:
  // seed state with `DEFAULT_THEME` (matching the SSR pass for
  // hydration-mismatch safety per AC-2) and read `localStorage` in a
  // single-shot `useEffect` after mount. The post-mount setState causes
  // a one-frame flicker from "system" to the stored value; per slice 170
  // P0-A5 / Notes-for-Implementing-Agent, that's acceptable below the
  // fold. See docs/audit-log/170-settings-theme-picker-hydration-decisions.md.
  const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);
  useEffect(() => {
    // Post-mount synchronization from a non-React state source
    // (localStorage) is the canonical pattern for this scenario; see
    // react.dev "synchronizing with external systems". The set-state runs
    // exactly once on mount and seeds the picker from the persisted
    // choice. Removing this would re-introduce the slice 170 hydration
    // bug. The react-hooks/set-state-in-effect rule is intentionally
    // disabled on the next line.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setTheme(readTheme(window.localStorage));
  }, []);

  function choose(next: Theme) {
    setTheme(next);
    if (typeof window !== "undefined") {
      writeTheme(window.localStorage, next);
      // Slice 170 contract: set `data-theme` on <html> -- the
      // ThemeAwareLogo (slice 176) keys off this attribute, and any
      // future consumer that prefers attribute-based reads stays
      // wire-compatible. P0-A1 (slice 203): data-theme remains the
      // source-of-truth; the class below is in ADDITION, not a
      // replacement.
      document.documentElement.setAttribute("data-theme", next);
      // Slice 203: write the `dark` class so the Tailwind v4 custom
      // variant `@custom-variant dark (&:is(.dark *))` configured in
      // `web/app/globals.css:5` matches and the `.dark { ... }` token
      // block at globals.css:86+ activates. Without this, the picker
      // persists a choice but the page never themes (the slice-170
      // deferred-work banner).
      const prefersDark = window.matchMedia(
        "(prefers-color-scheme: dark)",
      ).matches;
      applyThemeClass(document.documentElement, next, prefersDark);
    }
  }

  return (
    <div
      className="grid max-w-md grid-cols-3 gap-3"
      role="radiogroup"
      aria-label="Theme"
    >
      {THEMES.map((opt) => {
        const selected = theme === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => choose(opt.value)}
            data-testid={`settings-theme-option-${opt.value}`}
            data-selected={selected ? "true" : "false"}
            className={
              selected
                ? "rounded-md border-2 border-primary bg-primary/5 p-3 text-left"
                : "rounded-md border border-border bg-background p-3 text-left hover:border-foreground/40"
            }
          >
            <div
              className={`mb-2 h-12 rounded ${opt.swatch}`}
              aria-hidden="true"
              data-testid={`settings-theme-swatch-${opt.value}`}
            />
            <div className="text-sm font-medium">{opt.label}</div>
            <div className="text-xs text-muted-foreground">
              {opt.description}
            </div>
          </button>
        );
      })}
    </div>
  );
}
