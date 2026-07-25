// ATLAS-010 / AC-7 — page-specific `<title>` for the SCF catalog
// (/catalog/scf). Part of the pre-GA pass that makes every primary route
// carry the consistent "<Page> · security-atlas" document title (the
// /settings convention, slice 248). The page is a client component, so
// the metadata lives in this sibling server-component layout. The
// breadcrumb names this section "Catalog"; the title matches.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Catalog · security-atlas",
};

export default function CatalogScfLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
