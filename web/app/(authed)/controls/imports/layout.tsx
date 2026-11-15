// #1785 — document title for the control import pages. `controls/layout.tsx`
// sets a plain string title, which stops the root template reaching this
// segment, so the suffix is spelled out here (as in checklist/layout.tsx).
// Not in PAGE_TITLES: a key there adds a sidebar entry, and the import is
// reached from the Controls page.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: { absolute: "Import controls · security-atlas" },
};

export default function ControlImportsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
