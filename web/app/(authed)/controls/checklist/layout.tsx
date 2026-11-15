// #2081 — page-specific document title. The page is a client component,
// so it cannot export `metadata`; this layout carries it, rendered through
// the "<Page> · security-atlas" convention. Without it the page
// inherited "Controls" from `controls/layout.tsx`.

import type { Metadata } from "next";

import { pageTitle } from "@/lib/page-titles";

// `controls/layout.tsx` sets a plain string title, which stops the root
// template reaching this segment, so the suffix is spelled out here.
export const metadata: Metadata = {
  title: { absolute: `${pageTitle("/controls/checklist")} · security-atlas` },
};

export default function ChecklistLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return children;
}
