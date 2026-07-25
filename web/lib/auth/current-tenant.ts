// current-tenant.ts — slice 674 shared active-tenant-name resolution.
//
// Single source of truth for "what is the name of the tenant the caller
// is CURRENTLY scoped to". Every chrome surface that renders a tenant
// name — the dashboard H1 (`TenantContext`), the topbar `Breadcrumb`,
// and the `useCurrentTenantName` hook that drives both — resolves the
// name through these helpers, off the same `/api/me/tenants` payload the
// `TenantSwitcher` reads (slice 192 BFF, JWT-gated + RLS-backed).
//
// WHY THIS FILE EXISTS (slice 674): the H1 + breadcrumb already read the
// correct source, but each held a copy of the pick logic and — more
// importantly — fetched only once on mount. After an in-tab tenant
// switch the JWT cookie rotates and `router.refresh()` re-renders server
// components, but a mounted client component's mount effect does NOT
// re-run, so the H1/breadcrumb kept showing the origin ("Default Tenant")
// name while the switcher (which re-fetches on the slice-199
// `tenant-switched` broadcast) showed the new one. Consolidating the
// resolution here lets the shared hook re-fetch on that same broadcast,
// so every surface flips together.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//   - Invariant 6 (tenant isolation at the DB layer): the name is
//     derived from the bearer-gated `/v1/me/tenants` read; the client
//     never supplies, reads, or forwards a `tenant_id`. This module is
//     pure display logic over a server-authoritative payload.
//   - Anti-criterion (slice 674): display only. Nothing here touches
//     tenant-switch semantics or RLS scoping.

export interface Tenant {
  id: string;
  name: string;
  current: boolean;
}

export interface TenantsResponse {
  tenants?: Tenant[];
}

/**
 * pickCurrentTenantName returns the visible name of the tenant flagged
 * `current` in the list, or `null` when none is current / the name is
 * blank / the list is empty or null.
 *
 * The resolution follows the `current` FLAG, never a positional row —
 * that flag is the only thing that moves when the operator switches
 * tenants, and following position instead of the flag is exactly the
 * class of bug slice 674 closes.
 */
export function pickCurrentTenantName(tenants: Tenant[] | null): string | null {
  if (!tenants || tenants.length === 0) return null;
  const current = tenants.find((t) => t.current);
  if (!current) return null;
  const trimmed = current.name.trim();
  return trimmed.length === 0 ? null : trimmed;
}

/**
 * parseTenantsResponse narrows an untrusted `/api/me/tenants` JSON body
 * to the `Tenant[]` we render, or `null` when the payload is not the
 * expected shape. Defensive by construction so a malformed/transient
 * response collapses to silent absence rather than a thrown render.
 */
export function parseTenantsResponse(json: unknown): Tenant[] | null {
  if (typeof json !== "object" || json === null) return null;
  const tenants = (json as { tenants?: unknown }).tenants;
  if (!Array.isArray(tenants)) return null;
  return tenants as Tenant[];
}
