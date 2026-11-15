// #1802 — document title for /catalog/audited-under. The page is a client
// component, so the metadata lives in this server layout.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

export const metadata: Metadata = {
  title: pageTitle("/catalog/audited-under"),
};

export default function AuditedUnderLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
