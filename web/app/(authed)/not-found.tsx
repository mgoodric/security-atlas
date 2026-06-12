// Slice 672 — in-shell not-found boundary for the `(authed)` route group
// (AC-3, load-bearing).
//
// Before this file existed there was NO `not-found.tsx` anywhere under
// `web/app`, so any 404 inside the authed area (e.g. clicking a policy
// title that 404'd, or any `notFound()` call) rendered the framework's
// default shell-LESS 404 — no sidebar, no nav, only browser-back
// recovered, stranding the user (ATLAS-024 secondary finding).
//
// Because this boundary lives INSIDE the `(authed)` route group, Next's
// App Router wraps it in the group's `layout.tsx` (TopBar + Sidebar +
// skip-link). A `notFound()` thrown from any authed page — including the
// policy detail page on a genuinely-missing id — renders HERE, with the
// full app shell present and every nav affordance reachable. Recovery no
// longer requires the browser back button.

import Link from "next/link";

import { buttonVariants } from "@/components/ui/button";

export default function AuthedNotFound() {
  return (
    <div
      className="rounded-xl border bg-card py-16 px-6 text-center"
      data-testid="authed-not-found"
    >
      <div className="mx-auto mb-3 text-muted-foreground">
        <svg
          className="mx-auto h-12 w-12"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          aria-hidden
        >
          <path
            d="M9.75 9.75l4.5 4.5m0-4.5l-4.5 4.5M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </div>
      <h1 className="mb-1 text-sm font-semibold text-foreground">
        We couldn&apos;t find that page
      </h1>
      <p className="mb-4 text-xs text-muted-foreground">
        The page or record you were looking for doesn&apos;t exist, or you
        don&apos;t have access to it in this tenant. Use the navigation to keep
        going.
      </p>
      <Link
        href="/dashboard"
        className={buttonVariants({ size: "sm" })}
        data-testid="authed-not-found-cta"
      >
        Back to dashboard
      </Link>
    </div>
  );
}
