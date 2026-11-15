// #1818 — page-specific document title for /risks/taxonomy. The page is a
// client component, so it cannot export `metadata`; this layout carries it,
// as `risks/hierarchy/layout.tsx` does.

import type { Metadata } from "next";

import { pageTitle } from "@/lib/page-titles";

// `risks/layout.tsx` sets a plain string title, which stops the root
// template reaching this segment, so the suffix is spelled out here.
export const metadata: Metadata = {
  title: { absolute: `${pageTitle("/risks/taxonomy")} · security-atlas` },
};

export default function RiskTaxonomyLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
