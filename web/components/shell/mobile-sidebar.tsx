"use client";

// Slice 277 — mobile sidebar drawer.
//
// At viewport widths `< md` (Tailwind 768px), the desktop `<Sidebar>`
// (`web/components/shell/sidebar.tsx`) is hidden via `md:block`; this
// component takes over with a hamburger trigger that opens a slide-out
// `<Sheet>` containing the SAME nav items. Composition rules:
//
//   - Nav items come from the server (`getAuthedNav()` in the authed
//     layout) so the slice 186 admin-role gate runs once per request.
//     The drawer renders Link components from the resolved array; the
//     count badges (slice 214) render alongside by href match.
//   - State is component-local — no URL param, no persistence (AC-6).
//   - Closing: outside-click (via @base-ui/react Dialog default) +
//     nav-item click (we close on the Link's onClick) + Escape (the
//     dialog default). AC-6 + AC-7.
//   - The trigger itself carries `md:hidden` so the hamburger is
//     hidden at `≥ md` (AC-5).
//
// Accessibility: @base-ui/react Dialog implements the WAI-ARIA dialog
// pattern — focus traps to the popup, focus restores to the trigger
// on close, Escape closes, scroll-lock on body. The first nav item
// receives focus on open via the popup's natural focus-flow behavior
// (Title carries autoFocus in the base-ui defaults; we render a
// SheetTitle then the nav items, so Tab from the title lands on the
// first nav link).
//
// Constitutional invariants:
//   - Invariant 6 (tenant isolation): nav data comes from the same
//     `getAuthedNav()` call the desktop sidebar uses. No new tenant
//     scope, no new fetch surface.
//   - P0-277-2 / P0-277-3 / P0-277-5: this component renders on every
//     authed page (NOT a separate /m/ route, NOT a viewport-detecting
//     JS branch swapping component trees, NOT a User-Agent sniff). The
//     `md:hidden` Tailwind class on the trigger + the `hidden md:block`
//     on the desktop sidebar are the form-factor switch — both
//     produced by CSS media-queries at render time.

import { usePathname } from "next/navigation";
import Link from "next/link";
import { useCallback, useState } from "react";

import {
  ControlsCountBadge,
  RisksCountBadge,
} from "@/components/shell/sidebar-counts";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetPortal,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { cn } from "@/lib/utils";

/**
 * The serializable nav-data shape the server layout passes in. We
 * intentionally do NOT pipe the React-node `slot` of the desktop
 * `NavItem` through — that prop is built from server-mounted client
 * components (the badges) and forwarding it across the SC→CC boundary
 * for every render is wasteful. The drawer matches the slice 214
 * badge mounts to hrefs locally below.
 */
export type MobileNavItem = {
  href: string;
  label: string;
};

function badgeForHref(href: string) {
  switch (href) {
    case "/controls":
      return <ControlsCountBadge />;
    case "/risks":
      return <RisksCountBadge />;
    default:
      return null;
  }
}

export function MobileSidebar({ nav }: { nav: MobileNavItem[] }) {
  const [open, setOpen] = useState(false);
  const pathname = usePathname();

  // Close-on-click handler for the nav rows. The router transition
  // itself doesn't fire Sheet's outside-click (the popup contains the
  // clicked Link), so we close imperatively.
  const handleNavClick = useCallback(() => {
    setOpen(false);
  }, []);

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger
        // Slice 277 AC-3 / AC-5: the trigger is hidden at `≥ md` via
        // Tailwind's `md:hidden`. The desktop sidebar (rendered alongside
        // by `(authed)/layout.tsx`) carries `hidden md:block`; together
        // they produce exactly one chrome surface per viewport tier.
        className="md:hidden inline-flex h-9 w-9 items-center justify-center rounded-md text-foreground hover:bg-foreground/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        aria-label="Open navigation"
        data-testid="mobile-sidebar-trigger"
      >
        {/*
          Inline hamburger glyph — three stacked bars. Inline SVG instead
          of an icon library keeps the dependency footprint flat
          (P0-277-8) and matches the slice 213 / 214 pattern of "no
          icon library; lucide-style inline strokes where needed."
        */}
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <line x1="4" y1="6" x2="20" y2="6" />
          <line x1="4" y1="12" x2="20" y2="12" />
          <line x1="4" y1="18" x2="20" y2="18" />
        </svg>
      </SheetTrigger>
      <SheetPortal>
        <SheetContent side="left" data-testid="mobile-sidebar-drawer">
          <SheetHeader>
            <SheetTitle>Navigation</SheetTitle>
          </SheetHeader>
          <nav className="flex flex-col gap-1" aria-label="Mobile navigation">
            {nav.map((item) => {
              const isActive = pathname === item.href;
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  onClick={handleNavClick}
                  className={cn(
                    "flex items-center rounded-md px-3 py-2 text-sm font-medium transition-colors",
                    isActive
                      ? "bg-foreground/10 text-foreground"
                      : "text-muted-foreground hover:bg-foreground/5 hover:text-foreground",
                  )}
                  data-testid={`mobile-nav-link-${item.href.replace(
                    /\//g,
                    "_",
                  )}`}
                >
                  <span>{item.label}</span>
                  {badgeForHref(item.href)}
                </Link>
              );
            })}
          </nav>
        </SheetContent>
      </SheetPortal>
    </Sheet>
  );
}
