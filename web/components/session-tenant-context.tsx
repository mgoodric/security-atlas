"use client";

// #2399 — the session's tenant, for client caches keyed by tenant.
// `<AppShell>` reads it from the session cookie on the server, so every
// authenticated tree has it, and a tenant switch (router.refresh) or a
// new sign-in (a fresh server render) hands down the new one. Outside
// the shell, or when the cookie carries no tenant, it is undefined.

import { createContext, useContext, type ReactNode } from "react";

const SessionTenantContext = createContext<string | undefined>(undefined);

export function SessionTenantProvider({
  tenantId,
  children,
}: {
  tenantId: string | undefined;
  children: ReactNode;
}) {
  return (
    <SessionTenantContext.Provider value={tenantId}>
      {children}
    </SessionTenantContext.Provider>
  );
}

/** The session's tenant ID, or undefined when it is unknown. */
export function useSessionTenant(): string | undefined {
  return useContext(SessionTenantContext);
}
