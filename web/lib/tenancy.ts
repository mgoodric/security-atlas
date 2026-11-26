// #1764 — the tenant-count signal the web app uses to decide whether to
// show tenant chrome (ADR 0023 decision 2: tenant count, not a mode flag,
// drives the experience).
//
// The platform answers `GET /v1/me/tenancy` with the install's tenant
// count (#1763); the browser reads it through the `/api/me/tenancy` BFF.
// With one tenant the UI hides the Tenants and Super admins links, calls
// the tenant "your organisation", and assigns users to the sole tenant
// with no tenant field. With two or more, everything appears as before.
//
// This is presentation only. Authorization and RLS never branch on it;
// the hidden pages stay reachable and the server stays the authority.
//
// Unknown (loading, a failed probe, an older platform) shows the chrome:
// hiding it on a multi-tenant install would be the worse mistake, and an
// unknown answer then looks exactly like the UI before #1764.

import { useQuery } from "@tanstack/react-query";

export type Tenancy = {
  tenantCount: number;
  multiTenant: boolean;
};

export const TENANCY_QUERY_KEY = ["me", "tenancy"] as const;

/** parseTenancy reads the wire body; anything malformed is unknown. */
export function parseTenancy(body: unknown): Tenancy | undefined {
  if (typeof body !== "object" || body === null) return undefined;
  const b = body as { tenant_count?: unknown; multi_tenant?: unknown };
  if (typeof b.tenant_count !== "number") return undefined;
  if (typeof b.multi_tenant !== "boolean") return undefined;
  return { tenantCount: b.tenant_count, multiTenant: b.multi_tenant };
}

/** showTenantChrome is false only on an install known to have one tenant. */
export function showTenantChrome(t: Tenancy | undefined): boolean {
  return t === undefined || t.multiTenant;
}

export async function fetchTenancy(): Promise<Tenancy | null> {
  try {
    const res = await fetch("/api/me/tenancy", { cache: "no-store" });
    if (!res.ok) return null;
    return parseTenancy(await res.json()) ?? null;
  } catch {
    return null;
  }
}

/**
 * useTenancy returns the install's tenancy, or undefined while it is
 * loading or unknown. Client components only.
 */
export function useTenancy(): Tenancy | undefined {
  const q = useQuery({
    queryKey: TENANCY_QUERY_KEY,
    queryFn: fetchTenancy,
    staleTime: 60_000,
  });
  return q.data ?? undefined;
}
