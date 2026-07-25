// ATLAS-040 — page-specific `<title>` for /risks. The route had
// regressed to showing the raw URL as the document title; this restores
// the "<Page> · security-atlas" convention /settings established.
//
// `web/app/(authed)/risks/page.tsx` is a client component, and the
// Next.js App Router forbids exporting `metadata` from a client
// component. The canonical pattern (slice 248) is a sibling
// server-component layout that declares the metadata and passes
// `children` through unchanged.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Risks · security-atlas",
};

export default function RisksLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
