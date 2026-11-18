// #2080 — page-specific document title. The page is a client component,
// so it cannot export `metadata`; this layout carries it, rendered through
// the root layout's "%s · security-atlas" template.

import type { Metadata } from "next";

import { PlatformAdminGate } from "@/components/admin/platform-admin-gate";

export const metadata: Metadata = {
  title: "API keys",
};

export default function AdminApiKeysLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // #2503: a platform page; a tenant admin gets the platform 403 panel.
  return <PlatformAdminGate>{children}</PlatformAdminGate>;
}
