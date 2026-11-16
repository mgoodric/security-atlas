// ATLAS-010 / AC-7 — page-specific `<title>` for /questionnaires. Part
// of the pre-GA pass that makes every primary route carry the consistent
// "<Page> · security-atlas" document title (the /settings convention,
// slice 248). The page is a client component, so the metadata lives in
// this sibling server-component layout.
//
// #2170: the layout also gates the whole module (list + detail) on the
// Questionnaires module, server-side, before the client page mounts.

import type { Metadata } from "next";
import { pageTitle } from "@/lib/page-titles";

import { FeatureGate } from "@/components/feature-gate";

export const metadata: Metadata = {
  title: pageTitle("/questionnaires"),
};

export default function QuestionnairesLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <FeatureGate
      page="/questionnaires"
      moduleName={pageTitle("/questionnaires")}
    >
      {children}
    </FeatureGate>
  );
}
