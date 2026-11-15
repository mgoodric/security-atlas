// Issue #2176 — document title for /controls/needs-reanchoring. The page is
// a client component, so the metadata lives in this server layout.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Controls needing re-anchoring",
};

export default function NeedsReanchoringLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
