// OE-664 — page-specific document title for /personnel-security,
// mirroring the /action-plans layout convention (ATLAS-010). The page is
// a client component, so metadata lives in this sibling server-component
// layout.
//
// #2171: the layout also gates the whole module on its switch,
// server-side, before the client page mounts.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

import { FeatureGate } from "@/components/feature-gate";

export const metadata: Metadata = {
  title: pageTitle("/personnel-security"),
};

export default function PersonnelSecurityLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <FeatureGate
      page="/personnel-security"
      moduleName={pageTitle("/personnel-security")}
    >
      {children}
    </FeatureGate>
  );
}
