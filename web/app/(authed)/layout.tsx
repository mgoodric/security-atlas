import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { AppShell } from "@/components/shell/app-shell";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

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
  return <AppShell>{children}</AppShell>;
}
