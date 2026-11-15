// ATLAS-010 — page-specific `<title>` for the Vendor Claims module
// (/oscal/component-definitions). The route previously fell through to
// the bare "security-atlas" default; this gives it the consistent
// "<Page> · security-atlas" title. The user-facing module name is
// "Vendor Claims" (the page h1 and the breadcrumb agree), not the wire
// term "OSCAL component-definitions".
//
// The page is a client component, so the metadata is declared in this
// sibling server-component layout (the /settings pattern, slice 248).
//
// #2092: the layout also gates the whole module (list + per-import
// claims) on the `oscal.export` flag, server-side, before the client page
// mounts.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

import { FeatureGate } from "@/components/feature-gate";

export const metadata: Metadata = {
  title: pageTitle("/oscal/component-definitions"),
};

export default function VendorClaimsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <FeatureGate
      flag="oscal.export"
      moduleName={pageTitle("/oscal/component-definitions")}
    >
      {children}
    </FeatureGate>
  );
}
