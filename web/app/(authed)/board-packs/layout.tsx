// ATLAS-010 / AC-7 — page-specific `<title>` for /board-packs. Part of
// the pre-GA pass that makes every primary route carry the consistent
// "<Page> · security-atlas" document title (the /settings convention,
// slice 248). The page is a client component, so the metadata lives in
// this sibling server-component layout.
//
// #2092: the layout also gates the whole module (list + detail) on the
// `board.reporting` flag, server-side, before the client page mounts.

import type { Metadata } from "next";

import { FeatureGate } from "@/components/feature-gate";

export const metadata: Metadata = {
  title: "Board Packs",
};

export default function BoardPacksLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <FeatureGate flag="board.reporting" moduleName="Board packs">
      {children}
    </FeatureGate>
  );
}
