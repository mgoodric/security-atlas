// #1800 — document title for the requirement detail page
// (/requirements/[id]). The page is a client component, so its metadata
// lives in this server-component layout, as /catalog/scf does.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Requirement",
};

export default function RequirementsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
