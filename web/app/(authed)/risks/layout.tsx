// ATLAS-040 — page-specific `<title>` for /risks. The route had
// regressed to showing the raw URL as the document title; this restores
// the "<Page> · security-atlas" convention /settings established.
//
// `web/app/(authed)/risks/page.tsx` is a client component, and the
// Next.js App Router forbids exporting `metadata` from a client
// component. The canonical pattern (slice 248) is a sibling
// server-component layout that declares the metadata and passes
// `children` through unchanged.
//
// #2171: the layout also gates the whole module on its switch,
// server-side, before the client page mounts.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

import { FeatureGate } from "@/components/feature-gate";

export const metadata: Metadata = {
  title: pageTitle("/risks"),
};

export default function RisksLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <FeatureGate page="/risks" moduleName={pageTitle("/risks")}>
      {children}
    </FeatureGate>
  );
}
