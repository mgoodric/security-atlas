import type { ReactNode } from "react";

import Link from "next/link";

import { signOut } from "@/app/login/actions";
import { TenantSwitcher } from "@/components/auth/tenant-switcher";
import { Button } from "@/components/ui/button";
import { AccountMenu } from "@/components/shell/account-menu";
import { Breadcrumb } from "@/components/shell/breadcrumb";
import { GlobalSearch } from "@/components/shell/global-search";
import { InProgressAuditPill } from "@/components/shell/in-progress-audit-pill";
import { ThemeAwareLogo } from "@/components/shell/theme-aware-logo";
import { ThemeServerSync } from "@/components/shell/theme-server-sync";
import { UserAvatar, fetchUserIdentity } from "@/components/shell/user-avatar";

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
//
// Slice 277 — the topbar now accepts an optional `mobileSidebar` slot
// (a `<MobileSidebar>` element rendered by `(authed)/layout.tsx`). The
// element carries `md:hidden` on its trigger button, so the hamburger
// is visible only at viewport widths `< 768px`. Desktop UX at `≥ md`
// is unchanged — the trigger renders as a `display:none` element with
// no layout cost (P0-277-1).
//
// #2080 — the `v0 · self-host` chip moved out: the version tag lives in
// the sidebar footer only. From `md` up the bar stays on one line (it is
// checked at 1,200px): text never wraps, the right-hand cluster keeps
// its width, and the left cluster (logo + breadcrumb) is the one that
// gives way, truncating the breadcrumb rather than pushing the bar
// wider.
//
// #2213 — below `lg` (1,024px) the bar collapses so it fits a 390px
// phone and a 768px tablet without widening the page. Search folds
// behind an icon (`<GlobalSearch>` owns that), the avatar, name and
// Sign out fold into `<AccountMenu>`, and the audit pill keeps only its
// dot. Below `sm` the wordmark and breadcrumb drop too, leaving the
// hamburger and the mark on the left. With the right cluster that
// small it is pinned at every width now, and the left cluster is the
// one that gives way, as #2080 set up for `md` and wider.
export async function TopBar({ mobileSidebar }: { mobileSidebar?: ReactNode }) {
  const identity = await fetchUserIdentity();
  const signOutForm = (
    <form action={signOut}>
      <Button
        type="submit"
        variant="ghost"
        size="sm"
        className="w-full lg:w-auto"
      >
        Sign out
      </Button>
    </form>
  );

  return (
    <header
      data-testid="app-topbar"
      className="flex h-14 shrink-0 items-center justify-between gap-3 whitespace-nowrap border-b bg-background px-4 md:gap-4 md:px-6"
    >
      <div className="flex min-w-0 items-center gap-2 overflow-hidden md:gap-3">
        {/*
          Slice 277 — mobile sidebar trigger. The component carries
          `md:hidden` on its outer button, so this slot collapses to
          empty markup at `≥ md` (no layout footprint, no desktop
          regression). Mounted left-of-logo to match the canonical
          mobile-app convention (hamburger always on the leading edge).
        */}
        {mobileSidebar}
        <Link
          href="/dashboard"
          className="flex items-center gap-2 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          aria-label="security-atlas — go to dashboard"
        >
          <ThemeAwareLogo width={28} height={28} className="h-7 w-7" alt="" />
          <span className="hidden text-base font-semibold sm:inline">
            security-atlas
          </span>
        </Link>
        {/*
          Slice 223 — read-only breadcrumb chip `<tenant> > <page>`.
          Distinct from the TenantSwitcher (which is on the right side
          + interactive). Renders null when the tenant fetch fails
          (fail-quiet).
        */}
        <Breadcrumb className="hidden sm:flex" />
      </div>
      <div className="flex shrink-0 items-center gap-2 lg:gap-3">
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
        <div className="hidden items-center gap-3 lg:flex">
          <UserAvatar identity={identity} />
          {signOutForm}
        </div>
        {/*
          #2213 — below `lg`, the avatar and Sign out live behind one
          button. It renders even when `/v1/me` failed so Sign out is
          always reachable.
        */}
        <AccountMenu
          className="lg:hidden"
          name={identity?.name ?? null}
          initials={identity?.initials ?? null}
        >
          {signOutForm}
        </AccountMenu>
        {/*
          #2142 — the stored theme rides the same /v1/me read; the
          component renders nothing and re-applies it when it differs
          from this browser's cached choice.
        */}
        <ThemeServerSync theme={identity?.theme ?? null} />
      </div>
    </header>
  );
}
