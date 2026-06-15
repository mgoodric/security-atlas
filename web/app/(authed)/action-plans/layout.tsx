// Slice 384 — page-specific document title for /action-plans, mirroring the
// /exceptions layout convention (ATLAS-010). The page is a client component,
// so metadata lives in this sibling server-component layout.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Action Plans · security-atlas",
};

export default function ActionPlansLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
