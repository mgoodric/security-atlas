// #2119 — page-specific `<title>` for /approvals. The page is a client
// component, so the metadata lives in this sibling server-component
// layout. No module gate: the page spans four modules, and the platform
// leaves out the types whose module is switched off.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

export const metadata: Metadata = {
  title: pageTitle("/approvals"),
};

export default function ApprovalsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
