// Slice 060 — admin section shell + RBAC gate.
//
// AC-7: all /admin/* routes are gated by slice 035's `admin` role (which
// for the purposes of slice 060 binds to slice 034's `cred.IsAdmin` flag
// — the user-role grant table is not yet wired into the admin section).
// Non-admin signed-in users see a 403 explanation page, NOT a 404, so
// they can tell "this exists, I just can't reach it." Per the slice spec.
//
// The gate runs server-side on every render so a stale client-side cache
// can't bypass it. We hit the BFF /api/admin/me endpoint, which itself
// proxies to /v1/admin/credentials and reads the response code.
//
// Why two layouts (this + (authed)/layout.tsx)? Because the admin pages
// live OUTSIDE the (authed) route group — the slice spec calls the path
// `/admin/*` literally, not `/(authed)/admin/*`. We re-use the same
// TopBar + Sidebar shell here.

import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";
import Link from "next/link";

import { Sidebar } from "@/components/shell/sidebar";
import { TopBar } from "@/components/shell/topbar";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

async function isAdmin(bearer: string): Promise<boolean> {
  // Use the BFF self-introspection endpoint so the call resolves whether
  // we're rendering on the server (where NEXT_PUBLIC_API_BASE_URL points
  // at the platform) or in dev with the proxy. The BFF returns
  // { is_admin: bool } and never throws on 403 (just returns false).
  const h = await headers();
  const host = h.get("host") ?? "localhost:3000";
  const proto = h.get("x-forwarded-proto") ?? "http";
  const res = await fetch(`${proto}://${host}/api/admin/me`, {
    headers: { Cookie: `${ATLAS_JWT_COOKIE}=${bearer}` },
    cache: "no-store",
  });
  if (!res.ok) return false;
  const body = (await res.json()) as { is_admin?: boolean };
  return body.is_admin === true;
}

// Slice 278 — probe the demo-seed env-var gate so the breadcrumb
// renders the "Demo" link only when the feature is actually enabled.
// Fail-closed: any error returns false so the link doesn't appear.
async function isDemoSeedEnabled(bearer: string): Promise<boolean> {
  try {
    const h = await headers();
    const host = h.get("host") ?? "localhost:3000";
    const proto = h.get("x-forwarded-proto") ?? "http";
    const res = await fetch(`${proto}://${host}/api/admin/demo/status`, {
      headers: { Cookie: `${ATLAS_JWT_COOKIE}=${bearer}` },
      cache: "no-store",
    });
    if (!res.ok) return false;
    const body = (await res.json()) as { enabled?: boolean };
    return body.enabled === true;
  } catch {
    return false;
  }
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
  const admin = await isAdmin(bearer);
  // Only probe demo-seed enabled when the caller is admin — non-admins
  // never see the section anyway, and the probe would 403.
  const demoEnabled = admin ? await isDemoSeedEnabled(bearer) : false;

  return (
    <div className="flex h-screen flex-col">
      <TopBar />
      <div className="flex flex-1 overflow-hidden">
        <Sidebar />
        <main className="flex-1 overflow-y-auto p-4 sm:p-6">
          {admin ? (
            <AdminBreadcrumb demoEnabled={demoEnabled}>
              {children}
            </AdminBreadcrumb>
          ) : (
            <ForbiddenPanel />
          )}
        </main>
      </div>
    </div>
  );
}

function AdminBreadcrumb({
  children,
  demoEnabled,
}: {
  children: React.ReactNode;
  demoEnabled: boolean;
}) {
  return (
    <div className="space-y-4">
      <nav
        aria-label="Admin sections"
        className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
      >
        <Link href="/admin" className="hover:text-foreground">
          Admin
        </Link>
        <span>·</span>
        <Link href="/admin/sso" className="hover:text-foreground">
          SSO
        </Link>
        <Link href="/admin/users" className="hover:text-foreground">
          Users
        </Link>
        <Link href="/admin/api-keys" className="hover:text-foreground">
          API keys
        </Link>
        <Link href="/admin/features" className="hover:text-foreground">
          Features
        </Link>
        <Link href="/admin/audit" className="hover:text-foreground">
          Audit
        </Link>
        <Link href="/admin/super-admins" className="hover:text-foreground">
          Super admins
        </Link>
        <Link href="/admin/tenants" className="hover:text-foreground">
          Tenants
        </Link>
        {demoEnabled ? (
          <Link href="/admin/demo" className="hover:text-foreground">
            Demo
          </Link>
        ) : null}
      </nav>
      {children}
    </div>
  );
}

function ForbiddenPanel() {
  // AC-7: 403 message, NOT a 404. The page exists; this user lacks the
  // role. Tone matches the rest of the product — operator-friendly,
  // no error-code yelling.
  return (
    <div className="mx-auto max-w-2xl space-y-6 py-12">
      <Alert variant="destructive">
        <AlertTitle>This section is admin-only</AlertTitle>
        <AlertDescription>
          Your account is signed in but does not carry the{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">admin</code>{" "}
          role. Contact your tenant admin to request access. The admin section
          covers SSO setup, user role assignment, API key lifecycle, feature
          flag toggles, and the unified audit log.
        </AlertDescription>
      </Alert>
      <Card>
        <CardHeader>
          <CardTitle>Where to go from here</CardTitle>
          <CardDescription>
            Most day-to-day program work lives outside this section.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <p>
            <Link href="/dashboard" className="font-medium underline">
              Dashboard
            </Link>{" "}
            — program overview, controls, evidence freshness.
          </p>
          <p>
            <Link href="/controls" className="font-medium underline">
              Controls
            </Link>{" "}
            — the day-to-day surface for control owners.
          </p>
          <p>
            <Link href="/catalog/scf" className="font-medium underline">
              Catalog
            </Link>{" "}
            — SCF anchor browser (open to all signed-in users).
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
