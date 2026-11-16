// ATLAS-010 / AC-7 — page-specific `<title>` for /calendar. Part of the
// pre-GA pass that makes every primary route carry the consistent
// "<Page> · security-atlas" document title (the /settings convention,
// slice 248). The page is a client component, so the metadata lives in
// this sibling server-component layout.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

export const metadata: Metadata = {
  title: pageTitle("/calendar"),
};

export default function CalendarLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
