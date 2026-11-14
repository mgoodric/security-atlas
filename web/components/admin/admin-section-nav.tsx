// #1764 — the /admin section links. A client component so the Tenants
// and Super admins links can follow the install's tenant count
// (`lib/tenancy.ts`): with one tenant they are hidden; with two or more
// they appear. The pages themselves stay reachable and server-gated.

"use client";

import Link from "next/link";

import { showTenantChrome, useTenancy } from "@/lib/tenancy";

export function AdminSectionNav({ demoEnabled }: { demoEnabled: boolean }) {
  const tenantChrome = showTenantChrome(useTenancy());
  return (
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
      {tenantChrome ? (
        <>
          <Link href="/admin/super-admins" className="hover:text-foreground">
            Super admins
          </Link>
          <Link href="/admin/tenants" className="hover:text-foreground">
            Tenants
          </Link>
        </>
      ) : null}
      {demoEnabled ? (
        <Link href="/admin/demo" className="hover:text-foreground">
          Demo
        </Link>
      ) : null}
    </nav>
  );
}
