import Link from "next/link";

import { signOut } from "@/app/login/actions";
import { TenantSwitcher } from "@/components/auth/tenant-switcher";
import { Button } from "@/components/ui/button";
import { Breadcrumb } from "@/components/shell/breadcrumb";
import { GlobalSearch } from "@/components/shell/global-search";
import { InProgressAuditPill } from "@/components/shell/in-progress-audit-pill";
import { ThemeAwareLogo } from "@/components/shell/theme-aware-logo";
import { UserAvatar } from "@/components/shell/user-avatar";

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
//
// Slice 213 — header chrome parity gap. Two new affordances added to
// the right side of the topbar to close the parity gap surfaced by
// the slice 204 audit fleet:
//
//   - <InProgressAuditPill /> — client component that reads the
//     existing /api/audits BFF and renders an amber pill for the
//     most-recently-started period whose `status === "in_progress"`.
//     Returns null if zero match (P0-213-2 silent-absence).
//   - <UserAvatar /> — server component that reads /api/me via the
//     bearer cookie and renders initials + display name. Returns
//     null on any failure (fail-closed, parallels slice 186 sidebar
//     pattern).
//
// Slice 223 — closes the remaining two parity gaps from slice 213's
// deferred list (and supersedes spillover slices 271 + 272):
//
//   - <Breadcrumb /> — client component reading `usePathname()` +
//     `/api/me/tenants` (slice 192 BFF) to render `<tenant> > <page>`.
//     Read-only wayfinding; distinct from the interactive
//     TenantSwitcher. Returns null when either segment is missing
//     (fail-quiet, parallels slice 213's pill).
//   - <GlobalSearch /> — client component wrapping a ⌘K-focused
//     input + popover. Calls the BFF /api/search which forwards to
//     slice 268's unified `/v1/search` endpoint (merged on main).
//     Per-type results grouped (Controls / Risks / Evidence) with
//     keyboard navigation (arrows / Enter / Esc).
//
// See decisions log `docs/audit-log/223-controls-top-bar-chrome-
// decisions.md` D1 (subset shipped + spillovers superseded).
export async function TopBar() {
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
        {/*
          Slice 223 — read-only breadcrumb chip `<tenant> > <page>`.
          Distinct from the TenantSwitcher (which is on the right side
          + interactive). Renders null when the tenant fetch fails
          (fail-quiet).
        */}
        <Breadcrumb />
      </div>
      <div className="flex items-center gap-3">
        {/*
          Slice 223 — global ⌘K search input. Calls slice 268's
          /v1/search via the BFF /api/search; popover groups results
          by entity type with keyboard navigation.
        */}
        <GlobalSearch />
        {/*
          Slice 213 — in-progress audit pill. Reads /api/audits via
          TanStack Query (60s stale). Returns null when zero periods
          have status='in_progress' (P0-213-2 silent-absence).
        */}
        <InProgressAuditPill />
        {/*
          Slice 192: persistent multi-tenant switcher. Renders only
          when the operator has ≥2 tenants in their JWT's
          atlas:available_tenants[] claim (the component returns
          null otherwise — canvas §11 #13). The switcher fetches
          /api/me/tenants on mount and on a 60s interval (D1) to
          detect membership-removed transitions (P0-192-7).
        */}
        <TenantSwitcher />
        {/*
          Slice 213 — user avatar. Server component that reads
          /api/me via the bearer cookie. Fail-closed: returns null
          on any fetch / parse error (P0-213-4: no mock; the real
          user-context source is the single source of truth).
        */}
        <UserAvatar />
        <form action={signOut}>
          <Button type="submit" variant="ghost" size="sm">
            Sign out
          </Button>
        </form>
      </div>
    </header>
  );
}
