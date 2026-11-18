// #2135 — page-specific `<title>` for /needs-attention, the dashboard's
// "See all". The page is a client component, so the metadata lives in
// this sibling server-component layout. It is not a sidebar entry, so it
// is not in PAGE_TITLES (which is the sidebar's list); the root layout's
// template renders it as "Needs attention · security-atlas".

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Needs attention",
};

export default function NeedsAttentionLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
