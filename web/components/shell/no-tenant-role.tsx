// #2494 — the page a signed-in user with no role in the current tenant
// sees in place of the app. Since #2401 such a session derives no roles,
// so the dashboard would render a 403 in every panel. The `(authed)`
// layout renders this instead, from `has_tenant_role` on `/v1/me`.
//
// It is deliberately bare: no sidebar, search, audit pill or panels, so
// nothing on it calls a route the caller cannot read. It reads only
// `/v1/me/tenants` (the tenant switcher), which a role-less user may read
// (defaults.rego). The switcher renders only when the user belongs to
// another tenant. Sign out is always here.

import { signOut } from "@/app/login/actions";
import { TenantSwitcher } from "@/components/auth/tenant-switcher";
import { SignOutForm } from "@/components/shell/sign-out-form";
import { ThemeAwareLogo } from "@/components/shell/theme-aware-logo";
import { Button } from "@/components/ui/button";

export function NoTenantRole({
  tenantName,
  tenantCount,
}: {
  tenantName: string | null;
  tenantCount: number;
}) {
  const where = tenantName ?? "this organisation";
  return (
    <div className="flex min-h-screen flex-col">
      <header className="flex h-14 shrink-0 items-center justify-between gap-3 border-b bg-background px-4 md:px-6">
        <div className="flex items-center gap-2">
          <ThemeAwareLogo width={28} height={28} className="h-7 w-7" alt="" />
          <span className="hidden text-base font-semibold sm:inline">
            security-atlas
          </span>
        </div>
        <div className="flex items-center gap-2">
          <TenantSwitcher />
          <SignOutForm action={signOut}>
            <Button type="submit" variant="ghost" size="sm">
              Sign out
            </Button>
          </SignOutForm>
        </div>
      </header>
      <main
        id="main-content"
        className="flex flex-1 items-center justify-center p-4 sm:p-6"
      >
        <section
          data-testid="no-tenant-role"
          aria-labelledby="no-tenant-role-heading"
          className="flex max-w-md flex-col gap-3 text-center"
        >
          <h1 id="no-tenant-role-heading" className="text-xl font-semibold">
            You don&apos;t have a role in {where}
          </h1>
          <p className="text-sm text-muted-foreground">
            You are signed in, but nobody has given you a role here yet. Ask an
            admin of {where} to grant you one, then reload this page.
          </p>
          {tenantCount > 1 && (
            <p className="text-sm text-muted-foreground">
              You also belong to another organisation. Switch to it from the
              menu at the top of the page.
            </p>
          )}
        </section>
      </main>
    </div>
  );
}
