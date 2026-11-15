// #2080 — the one 404 body, shared by the `(authed)` not-found boundary
// (a `notFound()` thrown inside the group) and the root not-found (an
// unmatched URL, which never enters a route group). Both render it
// inside `<AppShell>` + `<PageShell>` for a signed-in user, so a 404
// keeps the sidebar and nav wherever it happens.
//
// The testids are slice 672's (`policy-detail.spec.ts` AC-3).

import Link from "next/link";

import { PageShell } from "@/components/shell/page-shell";
import { buttonVariants } from "@/components/ui/button";

export const NOT_FOUND_TITLE = "Page not found";

export function NotFoundPanel() {
  return (
    <PageShell title={NOT_FOUND_TITLE} width="reading">
      <div
        className="rounded-xl border bg-card px-6 py-16 text-center"
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
        <p className="mb-1 text-sm font-semibold text-foreground">
          We couldn&apos;t find that page
        </p>
        <p className="mb-4 text-xs text-muted-foreground">
          The page or record you were looking for doesn&apos;t exist, or you
          don&apos;t have access to it in this tenant. Use the navigation to
          keep going.
        </p>
        <Link
          href="/dashboard"
          className={buttonVariants({ size: "sm" })}
          data-testid="authed-not-found-cta"
        >
          Back to dashboard
        </Link>
      </div>
    </PageShell>
  );
}
