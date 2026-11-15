// #2081 — page-specific document title. The page is a client component,
// so it cannot export `metadata`; this layout carries it, rendered through
// the root layout's "%s · security-atlas" template. Without it the page
// inherited the register's title from `risks/layout.tsx`.

import type { Metadata } from "next";

import { pageTitle } from "@/lib/page-titles";

export const metadata: Metadata = {
  title: pageTitle("/risks/hierarchy"),
};

export default function RiskHierarchyLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
