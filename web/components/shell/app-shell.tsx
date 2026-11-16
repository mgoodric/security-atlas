// #2080 — AppShell: the authenticated chrome (skip link, top bar, sidebar,
// main column) in one place.
//
// Before #2080 the `(authed)` layout owned this markup and three other
// authenticated trees rebuilt part of it or none of it: `/admin` had the
// top bar and sidebar but no skip link or mobile drawer, `/audit` (the
// auditor workspace) had only the top bar, and `/audit-log` and the 404
// page rendered bare. Every one of them now renders through here, so a
// page cannot drift out of the shell by living outside `(authed)`.
//
// `bleed` drops the main column's padding and vertical scroll for the
// auditor workspace, whose three panes size themselves to the viewport
// and scroll independently.

import type { ReactNode } from "react";

import { MobileSidebar } from "@/components/shell/mobile-sidebar";
import { Sidebar, getAuthedNav } from "@/components/shell/sidebar";
import { TopBar } from "@/components/shell/topbar";
import { cn } from "@/lib/utils";

export async function AppShell({
  children,
  bleed = false,
}: {
  children: ReactNode;
  bleed?: boolean;
}) {
  // Slice 277 — resolve the nav groups server-side and hand them to the
  // client `<MobileSidebar>`, so the drawer applies exactly the gates the
  // desktop `<Sidebar>` does (#2081: both render `getAuthedNav()`).
  const nav = await getAuthedNav();

  return (
    <div className="flex h-screen flex-col">
      {/* Slice 359 (a11y A11Y-1): visually-hidden skip-link that
        becomes visible on focus. WCAG SC 2.4.1 Bypass Blocks (Level
        A). Lets keyboard-only users skip the TopBar + Sidebar (~25
        affordances) on every authed page navigation. Must remain
        the first focusable element inside the authed layout. */}
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:top-4 focus:left-4 focus:z-50 focus:rounded-md focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:shadow-lg focus:outline-none focus:ring-2 focus:ring-ring"
      >
        Skip to main content
      </a>
      <TopBar mobileSidebar={<MobileSidebar nav={nav} />} />
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <Sidebar />
        {/* min-w-0: without it this flex item's minimum width is its
          content's, so a wide table widens the column and the page
          scrolls sideways instead of the table's own container. */}
        <main
          id="main-content"
          tabIndex={-1}
          className={cn(
            "min-w-0 flex-1",
            bleed
              ? "flex flex-col overflow-hidden"
              : "overflow-y-auto p-4 sm:p-6",
          )}
        >
          {children}
        </main>
      </div>
    </div>
  );
}
