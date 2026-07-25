// ATLAS-010 / AC-7 — page-specific `<title>` for /activity. Part of the
// pre-GA pass that makes every primary route carry the consistent
// "<Page> · security-atlas" document title (the /settings convention,
// slice 248). The page shell is server-rendered; this sibling layout
// declares the metadata and passes `children` through unchanged.

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Activity · security-atlas",
};

export default function ActivityLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
