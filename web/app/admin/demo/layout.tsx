// #2503: /admin/demo seeds and tears down a whole tenant, an install-wide
// effect, so it is a platform page; a tenant admin gets the platform 403
// panel. The page sets its own metadata.

import { PlatformAdminGate } from "@/components/admin/platform-admin-gate";

export default function AdminDemoLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return <PlatformAdminGate>{children}</PlatformAdminGate>;
}
