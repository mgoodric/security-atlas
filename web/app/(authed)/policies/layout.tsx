// ATLAS-010 / AC-7 — page-specific `<title>` for /policies. Part of the
// pre-GA pass that makes every primary route carry the consistent
// "<Page> · security-atlas" document title (the /settings convention,
// slice 248). The page is a client component, so the metadata lives in
// this sibling server-component layout.
//
// #2171: the layout also gates the whole module on its switch,
// server-side, before the client page mounts.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

import { FeatureGate } from "@/components/feature-gate";

export const metadata: Metadata = {
  title: pageTitle("/policies"),
};

export default function PoliciesLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <FeatureGate page="/policies" moduleName={pageTitle("/policies")}>
      {children}
    </FeatureGate>
  );
}
