// Slice 060 — admin section shell + RBAC gate.
//
// AC-7: all /admin/* routes are gated on `is_admin` (the JWT middleware
// maps it from `atlas:super_admin` only) or, since #2503 (Matt,
// 2026-11-17), `is_tenant_admin`: a tenant admin by its live roles manages
// its own organisation's tenant-scoped pages (SSO, Users, Audit). The platform pages
// wrap their children in `<PlatformAdminGate>`, which admits `is_admin`
// alone. See docs/audit-log/2503-tenant-admin-pages-decisions.md.
// Non-admin signed-in users see a 403 explanation page, NOT a 404, so
// they can tell "this exists, I just can't reach it." Per the slice spec.
//
// The gate runs server-side on every render so a stale client-side cache
// can't bypass it. It reads `is_admin` and `is_tenant_admin` off the
// caller's own /v1/me record on the platform (OE-549 — previously via a
// self-fetch of the BFF's /api/admin/me, which is a thin proxy to that
// same endpoint).
//
// Why two layouts (this + (authed)/layout.tsx)? Because the admin pages
// live OUTSIDE the (authed) route group — the slice spec calls the path
// `/admin/*` literally, not `/(authed)/admin/*`. We re-use the same
// `<AppShell>` here (#2080 — previously a hand-rolled TopBar + Sidebar
// copy without the skip link or the mobile drawer).

import type { Metadata } from "next";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { AdminForbiddenPanel } from "@/components/admin/admin-forbidden";
import { AdminSectionNav } from "@/components/admin/admin-section-nav";
import { AppShell } from "@/components/shell/app-shell";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { fetchAdminMe, fetchDemoSeedEnabled } from "@/lib/api/self.server";
import { pageTitle } from "@/lib/page-titles";

// A plain `title` string here would also stop the root template applying
// to the admin subpages' own titles, so the section repeats the template.
export const metadata: Metadata = {
  title: { default: pageTitle("/admin"), template: "%s · security-atlas" },
};

// OE-549: both probes below used to self-fetch this app's own BFF routes
// at an origin rebuilt from the request Host header. That address does
// not exist inside the web container (the Host carries the EXTERNAL
// published port, and next-server binds the container IP rather than
// loopback), so every /admin/* page 500'd with an ECONNREFUSED in the
// shipped image while passing in `next dev`. They now reach the platform
// through the same stable internal base the rest of the app uses
// (`apiBaseURL()` -> ATLAS_HTTP_URL). See `lib/api/self.server.ts`.

type AdminAccess = "platform" | "tenant" | "none";

async function adminAccess(bearer: string): Promise<AdminAccess> {
  // Fail-closed: a 403, an unparseable body, or a transport error denies.
  // Denying is the correct posture AND strictly safer than the pre-OE-549
  // behavior, which let the transport error escape and crashed the route.
  const me = await fetchAdminMe(bearer);
  if (me.is_admin) return "platform";
  if (me.is_tenant_admin) return "tenant";
  return "none";
}

// Slice 278 — probe the demo-seed env-var gate so the breadcrumb
// renders the "Demo" link only when the feature is actually enabled.
// Fail-closed: any error returns false so the link doesn't appear.
async function isDemoSeedEnabled(bearer: string): Promise<boolean> {
  return fetchDemoSeedEnabled(bearer);
}

export default async function AdminLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    redirect("/login?from=/admin");
  }
  const access = await adminAccess(bearer);
  // Only probe demo-seed enabled for a super-admin: the demo page is a
  // platform page, and the probe would 403 for anyone else.
  const demoEnabled =
    access === "platform" ? await isDemoSeedEnabled(bearer) : false;

  return (
    <AppShell>
      {access === "none" ? (
        <AdminForbiddenPanel variant="section" />
      ) : (
        <div className="space-y-4">
          <AdminSectionNav
            demoEnabled={demoEnabled}
            platform={access === "platform"}
          />
          {children}
        </div>
      )}
    </AppShell>
  );
}
