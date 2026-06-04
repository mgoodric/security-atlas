// Slice 223 — shared-shell breadcrumb chip rendered in the topbar.
//
// Closes AC-7 (tenant breadcrumb chip "Sentinel Labs > Controls"
// rendered in the authed-layout header) and supersedes spillover
// slice 271. The breadcrumb chip is distinct from the TenantSwitcher
// — the switcher is an INTERACTIVE control (dropdown to pick a
// different tenant); the breadcrumb is READ-ONLY wayfinding ("you
// are here"). Mockup reference: `Plans/_archive/mockups/controls.html` lines
// 33-37.
//
// Why a client component: it reads `usePathname()` to derive the
// right-hand label and fetches `/api/me/tenants` (slice 192 BFF) for
// the left-hand tenant name. A server component would need a header
// injection from `proxy.ts` to learn the URL — adding a header for
// one read is more rope than a thin client component.
//
// Constitutional invariants:
//   * Invariant 6 (tenant isolation): tenant name comes from the
//     bearer-derived `/v1/me/tenants` read; no client-supplied tenant
//     context (P0-271-1 — no new platform endpoint).
//   * Non-interactive (P0-271-2): the breadcrumb segments are NOT
//     clickable. Tenant-switcher is the affordance for switching;
//     the breadcrumb is a label.
//   * Route-to-name map lives in `lib/page-names.ts` (P0-271-3 — not
//     hard-coded here). The pure helper is unit-tested in isolation.
//
// Failure modes (fail-quiet, parallels slice 213's pill):
//   * No bearer cookie / fetch error / non-200 → render `null`. A
//     chrome decoration must not surface a stack trace.
//   * Empty tenant list / no `current` tenant → render `null`. The
//     TenantSwitcher's eviction banner is the right surface for the
//     "removed from tenant" UX (P0-192-7), not the breadcrumb.

"use client";

import { useEffect, useState } from "react";
import { usePathname } from "next/navigation";

import { derivePageName } from "@/lib/page-names";

interface Tenant {
  id: string;
  name: string;
  current: boolean;
}

interface TenantsResponse {
  tenants?: Tenant[];
}

/**
 * pickCurrentTenantName returns the visible name of the tenant the
 * caller is currently scoped to, or `null` when no `current` entry
 * exists. Exported for unit coverage; not used outside this file
 * today.
 */
export function pickCurrentTenantName(tenants: Tenant[] | null): string | null {
  if (!tenants || tenants.length === 0) return null;
  const current = tenants.find((t) => t.current);
  if (!current) return null;
  const trimmed = current.name.trim();
  return trimmed.length === 0 ? null : trimmed;
}

export function Breadcrumb() {
  const pathname = usePathname() ?? "";
  const [tenantName, setTenantName] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const resp = await fetch("/api/me/tenants", {
          cache: "no-store",
          credentials: "include",
        });
        if (cancelled) return;
        if (!resp.ok) return;
        const data = (await resp.json()) as TenantsResponse;
        if (cancelled) return;
        setTenantName(pickCurrentTenantName(data?.tenants ?? null));
      } catch {
        // Fail quiet — keep the last good state.
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  const pageName = derivePageName(pathname);

  // Either segment missing renders nothing — the chrome stays honest
  // about its data sources.
  if (!tenantName || pageName.length === 0) return null;

  return (
    <nav
      data-testid="breadcrumb"
      aria-label="Breadcrumb"
      className="flex items-center gap-1 text-xs text-muted-foreground"
    >
      <span data-testid="breadcrumb-tenant">{tenantName}</span>
      <ChevronRight />
      <span
        data-testid="breadcrumb-page"
        className="text-foreground font-medium"
      >
        {pageName}
      </span>
    </nav>
  );
}

function ChevronRight() {
  return (
    <svg
      className="w-3 h-3"
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-hidden
    >
      <path d="M7.293 14.707a1 1 0 010-1.414L10.586 10 7.293 6.707a1 1 0 011.414-1.414l4 4a1 1 0 010 1.414l-4 4a1 1 0 01-1.414 0z" />
    </svg>
  );
}
