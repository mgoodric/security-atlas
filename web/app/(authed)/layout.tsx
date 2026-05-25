import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { MobileSidebar } from "@/components/shell/mobile-sidebar";
import { Sidebar, getAuthedNav } from "@/components/shell/sidebar";
import { TopBar } from "@/components/shell/topbar";
import { VersionFooter } from "@/components/version-footer";
import { SESSION_COOKIE } from "@/lib/auth";

export default async function AuthedLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const jar = await cookies();
  if (!jar.get(SESSION_COOKIE)?.value) {
    redirect("/login");
  }

  // Slice 277 — resolve the nav list ONCE per request server-side. The
  // desktop `<Sidebar>` renders its own copy from the same source via
  // `getAuthedNav()` (called inside the component); we pass a serialized
  // {href,label} array to the client `<MobileSidebar>` so the drawer
  // doesn't re-run the admin-role probe. Both surfaces honor the slice
  // 186 admin-role gate identically (no behavior drift between desktop
  // and mobile).
  const nav = await getAuthedNav();
  const mobileNav = nav.map(({ href, label }) => ({ href, label }));

  return (
    <div className="flex h-screen flex-col">
      <TopBar mobileSidebar={<MobileSidebar nav={mobileNav} />} />
      <div className="flex flex-1 overflow-hidden">
        <Sidebar />
        <main className="flex-1 overflow-y-auto p-6">{children}</main>
      </div>
      {/* Slice 072: build-version footer. Fixed-position; does not
        consume layout space. `print:hidden` keeps it off the
        board-pack print stylesheet (anti-criterion P0-A2). */}
      <VersionFooter />
    </div>
  );
}
