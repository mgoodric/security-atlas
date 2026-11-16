"use client";

import { useEffect, useState } from "react";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { useToast } from "@/components/ui/toast";

import { APIError } from "@/lib/api/base";
import { getMe, patchMe } from "@/lib/api/me";
import { applyThemeToDocument } from "@/lib/theme-class";

import { isCredentialBearer } from "../credential-bearer";
import { DEFAULT_THEME, readTheme, Theme, writeTheme } from "../theme";

// --- Section 2: Appearance ------------------------------------------------

// #2142: System first (it is the default), then Light, then Dark.
const THEMES: { value: Theme; label: string; description: string }[] = [
  { value: "system", label: "System", description: "Follow OS preference" },
  { value: "light", label: "Light", description: "Bright background" },
  { value: "dark", label: "Dark", description: "Low-light reading" },
];

// The profile query key the Profile tab already uses, so the two share one
// GET /v1/me.
const PROFILE_QUERY_KEY = ["settings-me-profile"] as const;

export function AppearanceSection() {
  const profileQuery = useQuery({
    queryKey: PROFILE_QUERY_KEY,
    queryFn: getMe,
  });
  // A credential bearer (API key, bootstrap admin) has no users row, so
  // PATCH /v1/me would 404: its choice lives in this browser only.
  const browserOnly = isCredentialBearer(profileQuery.data);
  return (
    <Card id="appearance" data-testid="settings-section-appearance">
      <CardHeader>
        <CardTitle>Appearance</CardTitle>
        <CardDescription data-testid="settings-appearance-description">
          {browserOnly
            ? "This credential has no user profile, so the theme is saved in this browser only."
            : "Saved to your account, so the theme follows you to other browsers."}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <AppearanceSelector
          storedTheme={profileQuery.data?.theme ?? null}
          browserOnly={browserOnly}
        />
      </CardContent>
    </Card>
  );
}

function AppearanceSelector({
  storedTheme,
  browserOnly,
}: {
  storedTheme: Theme | null;
  browserOnly: boolean;
}) {
  const { toast } = useToast();
  // Slice 170 D1: seed with DEFAULT_THEME to match the SSR pass, then read
  // the browser cache after mount. See
  // docs/audit-log/170-settings-theme-picker-hydration-decisions.md.
  const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);
  useEffect(() => {
    // Post-mount sync from a non-React source (localStorage); removing it
    // re-introduces the slice 170 hydration bug.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setTheme(readTheme(window.localStorage));
  }, []);
  // #2142: the account's stored choice outranks the browser cache once the
  // profile arrives. `<ThemeServerSync>` has already applied it to the page.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (storedTheme !== null) setTheme(storedTheme);
  }, [storedTheme]);

  const qc = useQueryClient();
  const save = useMutation({
    mutationFn: (next: Theme) => patchMe({ theme: next }),
    // Keep the shared profile cache current so a background refetch cannot
    // hand the picker the pre-PATCH theme.
    onSuccess: (profile) => qc.setQueryData(PROFILE_QUERY_KEY, profile),
    onError: (err) => {
      // 404: a credential bearer clicked before its profile loaded. There
      // is no account to save to, and the browser copy already stands.
      if (err instanceof APIError && err.status === 404) return;
      toast(
        "Couldn't save the theme to your account. It's set in this browser.",
      );
    },
  });

  function choose(next: Theme) {
    setTheme(next);
    // The browser cache drives first paint (the inline script in
    // web/app/layout.tsx); the account copy makes the choice per user.
    writeTheme(window.localStorage, next);
    applyThemeToDocument(next);
    if (!browserOnly) save.mutate(next);
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
            <ThemeSwatch theme={opt.value} />
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

// #2142: a swatch is a miniature of the app drawn in the theme's own tokens,
// not hand-picked colours. `.paper` and `.dark` (web/app/globals.css) each
// re-declare the full token set on their element, so `bg-background`,
// `bg-card` and `border-border` inside them resolve to that theme's values
// whatever theme the page itself is in. System shows both halves.
function ThemeSwatch({ theme }: { theme: Theme }) {
  return (
    <div
      className="mb-2 flex h-12 overflow-hidden rounded border border-border"
      aria-hidden="true"
      data-testid={`settings-theme-swatch-${theme}`}
    >
      {theme !== "dark" && <SwatchPane scope="paper" theme={theme} />}
      {theme !== "light" && <SwatchPane scope="dark" theme={theme} />}
    </div>
  );
}

function SwatchPane({
  scope,
  theme,
}: {
  scope: "paper" | "dark";
  theme: Theme;
}) {
  const pane = scope === "paper" ? "light" : "dark";
  return (
    <div
      className={`${scope} flex-1 bg-background p-1.5`}
      data-testid={`settings-theme-swatch-${theme}-${pane}`}
    >
      <div className="h-1.5 w-1/2 rounded-sm bg-foreground/70" />
      <div className="mt-1.5 h-4 rounded-sm border border-border bg-card" />
    </div>
  );
}
