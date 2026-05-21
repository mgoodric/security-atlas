import Link from "next/link";

import { signOut } from "@/app/login/actions";
import { TenantSwitcher } from "@/components/auth/tenant-switcher";
import { Button } from "@/components/ui/button";
import { ThemeAwareLogo } from "@/components/shell/theme-aware-logo";

// Slice 075 — the top-nav header. Replaces the slice-005 text placeholder
// ("security-atlas" span) with the canonical mark wrapped in a Link to
// /dashboard.
//
// Slice 176 — the inline `<picture media="(prefers-color-scheme: ...)">`
// element was replaced with `<ThemeAwareLogo>`. The `<picture>` element
// tracked the operating system's dark/light preference, which is NOT
// the same signal as the app theme picker (slice 170's persisted
// localStorage value). Operators on OS=dark with explicit app theme
// "light" were being served logo-dark.svg (near-white ink) onto a
// light app background and could not see the logo at all. The new
// component reads <html data-theme> (written by slice 170's
// AppearanceSelector) and falls back to prefers-color-scheme only
// when the theme is "system". See web/lib/theme-aware-logo.ts +
// docs/audit-log/176-logo-theme-coupling-decisions.md D1.
//
// AC-5 of slice 075: 24-32px logo height. We use h-7 (28px) — sits
// inside the h-14 (56px) top-bar with breathing room above + below.
// The Link wrapper makes the logo a click-target back to /dashboard
// (the same anchor an authenticated user expects when clicking the
// brand mark of any SaaS web app).
//
// The "security-atlas" wordmark stays as text next to the mark so the
// header still works for screen readers and for users on text-mode
// browsers (the mark's <img alt> is decorative-of-the-wordmark; the
// wordmark itself carries the brand name).
export function TopBar() {
  return (
    <header className="flex h-14 shrink-0 items-center justify-between border-b bg-background px-6">
      <div className="flex items-center gap-3">
        <Link
          href="/dashboard"
          className="flex items-center gap-2 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          aria-label="security-atlas — go to dashboard"
        >
          <ThemeAwareLogo width={28} height={28} className="h-7 w-7" alt="" />
          <span className="text-base font-semibold">security-atlas</span>
        </Link>
        <span className="text-xs text-muted-foreground">v0 · self-host</span>
      </div>
      <div className="flex items-center gap-3">
        {/*
          Slice 192: persistent multi-tenant switcher. Renders only
          when the operator has ≥2 tenants in their JWT's
          atlas:available_tenants[] claim (the component returns
          null otherwise — canvas §11 #13). The switcher fetches
          /api/me/tenants on mount and on a 60s interval (D1) to
          detect membership-removed transitions (P0-192-7).
        */}
        <TenantSwitcher />
        <form action={signOut}>
          <Button type="submit" variant="ghost" size="sm">
            Sign out
          </Button>
        </form>
      </div>
    </header>
  );
}
