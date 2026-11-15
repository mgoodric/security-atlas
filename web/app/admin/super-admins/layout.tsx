// #2080 — page-specific document title. The page is a client component,
// so it cannot export `metadata`; this layout carries it, rendered through
// the root layout's "%s · security-atlas" template.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Super admins",
};

export default function AdminSuperAdminsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
