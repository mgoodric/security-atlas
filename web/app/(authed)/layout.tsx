import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { ModulesProvider } from "@/components/modules-context";
import { AppShell } from "@/components/shell/app-shell";
import { NoTenantRole } from "@/components/shell/no-tenant-role";
import { ToastProvider } from "@/components/ui/toast";
import {
  fetchTenantRoleState,
  fetchTenantsSummary,
} from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { fetchEffectiveModules } from "@/lib/feature-nav.server";

export default async function AuthedLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    redirect("/login");
  }

  // #2494 — a signed-in user with no role in this tenant gets one page
  // that says so, not a dashboard of 403s. The layout persists across
  // client navigation, so this runs once per load; a role granted since
  // shows on the next reload (live roles, #2442). A failed read is
  // "unknown" and renders the app as before, where the page shows its own
  // error state: it is never taken as role-less, nor as having a role.
  if ((await fetchTenantRoleState(bearer)) === "no_role") {
    const tenants = await fetchTenantsSummary(bearer);
    return (
      <NoTenantRole
        tenantName={tenants.currentName}
        tenantCount={tenants.count}
      />
    );
  }

  // #2080 — the chrome (skip link, top bar, sidebar with the version tag
  // in its footer, main column) lives in `<AppShell>`, shared with
  // `/admin`, `/audit`, `/audit-log` and the 404 page.
  //
  // #2089: the toast provider lives in this persistent layout, so a toast
  // raised before router.push shows on the landing page.
  //
  // #2174: the caller's effective modules reach client pages through
  // `<ModulesProvider>`, so a record hides its link sections into a
  // module the caller cannot see.
  const modules = await fetchEffectiveModules();
  return (
    <AppShell>
      <ModulesProvider modules={modules}>
        <ToastProvider>{children}</ToastProvider>
      </ModulesProvider>
    </AppShell>
  );
}
