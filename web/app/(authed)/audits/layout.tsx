// ATLAS-010 — page-specific `<title>` for /audits.
//
// `web/app/(authed)/audits/page.tsx` is a client component, and the
// Next.js App Router forbids exporting `metadata` from a client
// component. The canonical pattern (established by /settings, slice 248)
// is a sibling server-component layout whose only job is to declare the
// page-specific metadata and pass `children` through unchanged.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Audits · security-atlas",
};

export default function AuditsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
