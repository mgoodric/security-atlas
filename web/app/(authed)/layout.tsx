import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { ModulesProvider } from "@/components/modules-context";
import { AppShell } from "@/components/shell/app-shell";
import { ToastProvider } from "@/components/ui/toast";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { fetchEffectiveModules } from "@/lib/feature-nav.server";

export default async function AuthedLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  if (!jar.get(ATLAS_JWT_COOKIE)?.value) {
    redirect("/login");
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
