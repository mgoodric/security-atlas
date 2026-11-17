"use client";

// #2174 — the caller's effective modules, for client pages that link
// across modules. The authed layout reads `GET /v1/me/modules` on the
// server (the read the sidebar makes) and provides it here; a record page
// asks `useSeesPage("/policies")` before it renders, or fetches, a link
// section into that module. The web app keeps no module map of its own:
// which module owns a page comes from the server's registry.

import { createContext, useContext, type ReactNode } from "react";

import { type EffectiveModules, seesPage } from "@/lib/feature-nav";

const ModulesContext = createContext<EffectiveModules | undefined>(undefined);

export function ModulesProvider({
  modules,
  children,
}: {
  modules: EffectiveModules;
  children: ReactNode;
}) {
  return (
    <ModulesContext.Provider value={modules}>
      {children}
    </ModulesContext.Provider>
  );
}

/** Whether the caller may see links into the module that owns `page`. */
export function useSeesPage(page: string): boolean {
  return seesPage(useContext(ModulesContext), page);
}
