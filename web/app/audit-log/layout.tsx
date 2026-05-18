// Slice 125 — server-component layout for /audit-log.
//
// AC-5 + P0-A2. The route lives OUTSIDE the slice-060 `/admin/*` tree on
// purpose: the slice doc calls the path `/audit-log` literally, and the
// page is reachable to admins + auditors (slice-124 OPA gate) — wider
// than the strict admin-only `/admin/*` group.
//
// Two layers of defense:
//   1. proxy.ts (Next 16 request interceptor) gates ALL non-exempt paths
//      on the session cookie; unauthenticated traffic gets bounced to
//      /login before this layout renders.
//   2. THIS LAYOUT issues a /api/admin/me preflight on the server (so a
//      stale client-side cache cannot bypass it). Non-admin signed-in
//      users get a server-side `redirect("/dashboard?error=admin-only")`,
//      preserving the slice-spec's redirect-with-error UX.
//
// The backend (slice-124) is the third leg of the defense — it rejects
// non-admin/auditor/grc_engineer bearers with 403 regardless of which UI
// reaches it. The route guard here is the FIRST line; the BFF / backend
// 403 is defense-in-depth.
//
// Auditor-only callers: the /api/admin/me endpoint as shipped (slice 060)
// returns only { is_admin }. To extend access to auditors and grc_engineers
// without a server-side role probe, a sibling slice extends /api/admin/me
// with a role list — see slice 125's decisions log D9. Until then, /audit-log
// gates on `is_admin === true` strictly; backend 403 catches the rest.

import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";

import { SESSION_COOKIE } from "@/lib/auth";

async function isAdmin(bearer: string): Promise<boolean> {
  const h = await headers();
  const host = h.get("host") ?? "localhost:3000";
  const proto = h.get("x-forwarded-proto") ?? "http";
  const res = await fetch(`${proto}://${host}/api/admin/me`, {
    headers: { Cookie: `${SESSION_COOKIE}=${bearer}` },
    cache: "no-store",
  });
  if (!res.ok) return false;
  const body = (await res.json()) as { is_admin?: boolean };
  return body.is_admin === true;
}

export default async function AuditLogLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    redirect("/login?from=/audit-log");
  }
  const admin = await isAdmin(bearer);
  if (!admin) {
    redirect("/dashboard?error=admin-only");
  }
  return <>{children}</>;
}
