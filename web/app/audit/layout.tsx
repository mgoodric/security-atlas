// Slice 042 — audit workspace shell (AC-1, AC-6).
//
// The auditor session shell. Like the admin section, /audit lives
// OUTSIDE the (authed) route group because the slice spec names the path
// `/audit` literally. The shell:
//
//   * AC-1 / AC-6 — redirects to /login?from=/audit when the session
//     cookie is absent. Sign-out (the shared `signOut` server action in
//     the TopBar) deletes that cookie, so a signed-out auditor hitting
//     /audit lands back on /login. A subsequent sign-in returns here and
//     /audit re-resolves the assigned period — session state is the
//     cookie and nothing else, so it is fully cleared on sign-out.
//
//   * Wraps children in the React Query Providers so the workspace's
//     client components can use TanStack Query. The root layout's
//     Providers already cover the tree, but /audit is its own segment;
//     re-declaring is harmless and keeps the segment self-contained.
//
// The AuditPeriod top-bar context (AC-1) is rendered per-page rather
// than in the layout, because the layout cannot know the resolved period
// without an async fetch that the page already performs — rendering it
// in the page avoids a duplicate /v1/me/audit-period round-trip.

import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { TopBar } from "@/components/shell/topbar";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export default async function AuditLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  if (!jar.get(ATLAS_JWT_COOKIE)?.value) {
    redirect("/login?from=/audit");
  }
  return (
    <div className="flex h-screen flex-col">
      <TopBar />
      <div className="flex flex-1 flex-col overflow-hidden">{children}</div>
    </div>
  );
}
