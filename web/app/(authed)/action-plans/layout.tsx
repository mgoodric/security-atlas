// Slice 384 — page-specific document title for /action-plans, mirroring the
// /exceptions layout convention (ATLAS-010). The page is a client component,
// so metadata lives in this sibling server-component layout.
//
// #2171: the layout also gates the whole module on its switch,
// server-side, before the client page mounts.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

import { FeatureGate } from "@/components/feature-gate";

export const metadata: Metadata = {
  title: pageTitle("/action-plans"),
};

export default function ActionPlansLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <FeatureGate page="/action-plans" moduleName={pageTitle("/action-plans")}>
      {children}
    </FeatureGate>
  );
}
