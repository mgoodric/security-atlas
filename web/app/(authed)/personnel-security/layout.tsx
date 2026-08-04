// OE-664 — page-specific document title for /personnel-security,
// mirroring the /action-plans layout convention (ATLAS-010). The page is
// a client component, so metadata lives in this sibling server-component
// layout.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Personnel Security · security-atlas",
};

export default function PersonnelSecurityLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
