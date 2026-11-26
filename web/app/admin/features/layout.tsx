// #2080 — page-specific document title. The page is a client component,
// so it cannot export `metadata`; this layout carries it, rendered through
// the root layout's "%s · security-atlas" template.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Features",
};

export default function AdminFeaturesLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // #2503: a tenant-scoped page; the /admin layout admits a tenant admin.
  return children;
}
