// #2503 — the server-side gate for the platform /admin pages.
//
// The /admin layout admits a super-admin (`is_admin`) and a tenant admin
// (`is_tenant_admin`). The tenant admin manages only the tenant-scoped
// pages (SSO, Users, Features, Audit); every other page wraps its children in this gate, which
// renders the platform 403 panel unless the caller is a super-admin. The
// platform's handlers and OPA refuse a tenant admin on those routes
// anyway; this keeps the page from rendering a screen of 403s.
//
// Fail-closed: `fetchAdminMe` maps any failure to `is_admin: false`.

import { cookies } from "next/headers";

import { AdminForbiddenPanel } from "@/components/admin/admin-forbidden";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { fetchAdminMe } from "@/lib/api/self.server";

export async function PlatformAdminGate({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  const me = bearer ? await fetchAdminMe(bearer) : null;
  if (!me?.is_admin) {
    return <AdminForbiddenPanel variant="platform" />;
  }
  return <>{children}</>;
}
