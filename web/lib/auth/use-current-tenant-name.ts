// use-current-tenant-name.ts — slice 674 shared active-tenant-name hook.
//
// The single React entry point every chrome surface uses to render the
// name of the tenant the operator is CURRENTLY scoped to. Replaces the
// per-component, mount-only fetch that the dashboard H1 (`TenantContext`)
// and the topbar `Breadcrumb` each carried.
//
// LIFECYCLE (the fix):
//   1. Fetch `/api/me/tenants` on mount (slice 192 BFF; JWT-gated,
//      RLS-backed) and resolve the `current` tenant's name.
//   2. Subscribe to the slice-199 `tenant-switched` BroadcastChannel and
//      re-fetch on receipt. This is the SAME signal the `TenantSwitcher`
//      acts on — so when the operator switches tenants in this tab
//      (onPick posts the broadcast before router.refresh()) or any
//      sibling tab, every name surface re-resolves together instead of
//      holding the origin tenant name until a hard reload.
//
// Why a broadcast subscription and not router.refresh() alone:
// router.refresh() re-renders SERVER components, but a mounted client
// component's mount effect does not re-run — so a fetch-on-mount surface
// stays stale across an in-tab switch. The broadcast is the existing,
// purpose-built nudge for exactly this (slice 199).
//
// Fail-quiet: any fetch/parse failure keeps the last good name (or stays
// undefined on first load) — chrome decoration must never surface a
// stack trace. Mirrors the switcher's "don't blow away the list on a
// transient failure" stance.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//   - Invariant 6 (tenant isolation): name comes from the bearer-gated
//     `/v1/me/tenants` read; no client-supplied tenant context.
//   - slice 674 anti-criterion: display only; no switch-semantics / RLS
//     change. The broadcast carries only an opaque nudge — the hook
//     re-validates by re-fetching the RLS-backed endpoint (P0-199-2).

"use client";

import { useEffect, useState } from "react";

import { onTenantSwitched } from "@/lib/auth/tenant-broadcast";
import {
  parseTenantsResponse,
  pickCurrentTenantName,
} from "@/lib/auth/current-tenant";

/**
 * useCurrentTenantName returns the name of the tenant the caller is
 * currently scoped to, or `null` when it cannot be resolved (loading,
 * fetch failure, no `current` entry, or a blank name). Re-resolves on
 * the `tenant-switched` broadcast so the value tracks the active tenant
 * across an in-tab or cross-tab switch without a manual reload.
 */
export function useCurrentTenantName(): string | null {
  const [tenantName, setTenantName] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    const load = async () => {
      try {
        const resp = await fetch("/api/me/tenants", {
          cache: "no-store",
          credentials: "include",
        });
        if (cancelled || !resp.ok) return;
        const data = (await resp.json()) as unknown;
        if (cancelled) return;
        setTenantName(pickCurrentTenantName(parseTenantsResponse(data)));
      } catch {
        // Fail quiet — keep the last good name.
      }
    };

    void load();

    // Re-fetch when any tab broadcasts a tenant switch (slice 199). The
    // switcher's onPick posts this before router.refresh(), so the
    // in-tab switch path nudges this surface too.
    const unsubscribe = onTenantSwitched(() => {
      void load();
    });

    return () => {
      cancelled = true;
      unsubscribe();
    };
  }, []);

  return tenantName;
}
