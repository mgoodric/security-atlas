// #2080 — page-specific document title. The page is a client component,
// so it cannot export `metadata`; this layout carries it, rendered through
// the root layout's "%s · security-atlas" template.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

export const metadata: Metadata = {
  title: pageTitle("/csf"),
};

export default function CsfLayout({ children }: { children: React.ReactNode }) {
  return children;
}
