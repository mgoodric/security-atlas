// #2847 — document title for the risk import pages. `risks/layout.tsx`
// sets a plain string title, which stops the root template reaching this
// segment, so the suffix is spelled out here (as in controls/imports).
// Not in PAGE_TITLES: a key there adds a sidebar entry, and the import is
// reached from the Risks page.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: { absolute: "Import risks · security-atlas" },
};

export default function RiskImportsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
