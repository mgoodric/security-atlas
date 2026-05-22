// tenant-switcher.tsx — slice 192 multi-tenant header dropdown.
//
// Renders a Stripe/Linear-style dropdown showing the operator's
// current tenant + the other available tenants (sourced from the
// JWT's atlas:available_tenants[] claim via GET /v1/me/tenants).
// On selection, calls switchTenant() which exchanges the JWT for
// one scoped to the target tenant; on success the page refreshes
// so server components re-render with the new tenant scope.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-192-1 / canvas §11 #13: returns NULL when
//     tenants.length <= 1. The UI never shows tenant chrome to a
//     single-tenant operator. The hide rule is the LOAD-BEARING UX
//     rule — `if (tenants.length <= 1) return null;` is the
//     entire enforcement.
//   - P0-192-3: switching ALWAYS goes through `switchTenant()`
//     which calls /oauth/token via the BFF — never a custom
//     session-mutate endpoint.
//   - P0-192-7: membership-removed UX is non-optional — the banner
//     surfaces when the periodic re-fetch reveals the
//     current_tenant has been removed from the available set.
//   - P0-192-8: eviction-is-eventual — banner copy explicitly
//     names the contract operators see ("Your access has been
//     removed; switch to another tenant or sign out").
//
// PERIODIC RE-FETCH (D1 = 60s): the dropdown re-fetches
// /api/me/tenants every 60 seconds while the tab is foregrounded.
// Backgrounded tabs pause to avoid surplus network traffic; the
// next foreground triggers an immediate fetch so the operator
// sees fresh state quickly.

"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { switchTenant } from "@/lib/auth/switch-tenant";
import {
  onTenantSwitched,
  postTenantSwitched,
} from "@/lib/auth/tenant-broadcast";

const REFETCH_INTERVAL_MS = 60 * 1000; // D1 — 60s cache

interface Tenant {
  id: string;
  name: string;
  current: boolean;
}

// tenantsEqual reports whether two tenant lists are identical by
// (id, name, current) tuple in order. Used to short-circuit the
// state update when a periodic re-fetch returns the same list.
function tenantsEqual(a: Tenant[] | null, b: Tenant[]): boolean {
  if (a === null) return false;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (
      a[i].id !== b[i].id ||
      a[i].name !== b[i].name ||
      a[i].current !== b[i].current
    ) {
      return false;
    }
  }
  return true;
}

interface TenantsResponse {
  tenants: Tenant[];
}

export function TenantSwitcher() {
  const router = useRouter();
  const [tenants, setTenants] = useState<Tenant[] | null>(null);
  const [open, setOpen] = useState(false);
  const [switching, setSwitching] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [evicted, setEvicted] = useState(false);
  const containerRef = useRef<HTMLDivElement | null>(null);
  // Holds the latest fetchTenants closure so out-of-effect callers
  // (the slice 199 broadcast subscriber below) can request an
  // immediate re-fetch without duplicating the fetch logic.
  const fetchTenantsRef = useRef<(() => Promise<void>) | null>(null);

  // Initial fetch + periodic re-fetch (D1: every 60s when
  // foregrounded; pause when backgrounded).
  //
  // The fetchTenants closure is DEFINED inside the effect — not
  // hoisted via useCallback — so the react-hooks/set-state-in-effect
  // rule sees the setState calls as callback-driven (from
  // setInterval / visibilitychange) rather than as a top-level
  // effect side-effect. The setInterval bump is the external
  // subscription the rule's documentation endorses.
  useEffect(() => {
    let cancelled = false;
    const fetchTenants = async () => {
      try {
        const resp = await fetch("/api/me/tenants", {
          cache: "no-store",
          credentials: "include",
        });
        if (cancelled) return;
        if (!resp.ok) {
          // Don't blow away the existing list on a transient failure.
          return;
        }
        const data = (await resp.json()) as TenantsResponse;
        if (cancelled) return;
        const next = Array.isArray(data?.tenants) ? data.tenants : [];
        // Avoid re-rendering when the list is unchanged — the
        // periodic 60s re-fetch returns the same shape on every
        // tick in the steady state, and React's reference-equality
        // check would otherwise tear down + re-mount the dropdown
        // children on every interval.
        setTenants((prev) => (tenantsEqual(prev, next) ? prev : next));
        const stillHasCurrent = next.some((t) => t.current);
        setEvicted(next.length > 0 && !stillHasCurrent);
      } catch {
        // ignore — keep the last good list visible
      }
    };

    // Expose the latest fetchTenants closure to out-of-effect
    // callers (slice 199 broadcast subscriber). Updated on every
    // mount so a stale closure cannot fire against a torn-down
    // tab.
    fetchTenantsRef.current = fetchTenants;

    // Kick off the first fetch via queueMicrotask so the setState
    // calls happen in a microtask callback, not synchronously in
    // the effect body.
    queueMicrotask(fetchTenants);
    let interval: ReturnType<typeof setInterval> | null = null;

    const start = () => {
      if (interval) return;
      interval = setInterval(fetchTenants, REFETCH_INTERVAL_MS);
    };
    const stop = () => {
      if (interval) {
        clearInterval(interval);
        interval = null;
      }
    };
    const onVisibility = () => {
      if (document.hidden) {
        stop();
      } else {
        fetchTenants();
        start();
      }
    };

    if (!document.hidden) start();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      cancelled = true;
      stop();
      document.removeEventListener("visibilitychange", onVisibility);
      fetchTenantsRef.current = null;
    };
  }, []);

  // Slice 199 — cross-tab BroadcastChannel sync.
  //
  // When a sibling tab successfully switches tenants, it posts a
  // `tenant-switched` message on the `atlas-tenant` channel. This
  // effect subscribes, and on receipt:
  //
  //   1. Re-fetches /api/me/tenants so the dropdown's current-tenant
  //      flag updates immediately (no waiting on the 60s tick).
  //   2. Calls router.refresh() so server components re-render
  //      against the new JWT cookie (cookies are shared per origin,
  //      so the sibling's token-exchange has already replaced this
  //      tab's atlas_jwt cookie before the broadcast arrived).
  //
  // The subscriber does NOT call postTenantSwitched — that would
  // create the infinite loop P0-199-4 forbids. The post path is
  // only invoked from onPick (a user action), never from a receive.
  //
  // The helper is a no-op in SSR / older browsers (P0-199-1); the
  // returned unsubscribe is callable unconditionally.
  useEffect(() => {
    const unsub = onTenantSwitched(() => {
      // Don't trust the broadcast payload (P0-199-2). The receive
      // handler re-validates by re-fetching the JWT-gated, RLS-
      // backed /api/me/tenants and rendering from that.
      const refetch = fetchTenantsRef.current;
      if (refetch) {
        void refetch();
      }
      router.refresh();
    });
    return unsub;
  }, [router]);

  // Close on outside click + Escape.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const currentTenant = useMemo(
    () => (tenants ? tenants.find((t) => t.current) : undefined),
    [tenants],
  );

  const onPick = useCallback(
    async (targetId: string) => {
      if (switching) return;
      setErrorMsg(null);
      setSwitching(true);
      try {
        const result = await switchTenant(targetId);
        if (!result.ok) {
          setErrorMsg(result.message);
          setSwitching(false);
          return;
        }
        setOpen(false);
        // Slice 199 — notify sibling tabs in the same origin that
        // the tenant switched. Same-origin scope by construction
        // (BroadcastChannel). Graceful no-op when the API is
        // unavailable. MUST run before router.refresh() so the
        // broadcast goes out promptly — router.refresh() does not
        // block on it, but the ordering documents intent.
        postTenantSwitched(targetId);
        // Hard refresh so server components re-render with the new
        // tenant scope (the JWT cookie carries it).
        router.refresh();
      } finally {
        setSwitching(false);
      }
    },
    [router, switching],
  );

  // P0-192-1 + canvas §11 #13: hide chrome when single-tenant.
  // Also hide while loading the initial fetch — better than a
  // flicker of the dropdown that then collapses to nothing.
  if (tenants === null) {
    return null;
  }
  if (tenants.length <= 1) {
    return null;
  }

  return (
    <div className="flex flex-col items-end gap-2">
      {evicted ? (
        <MembershipRemovedBanner
          firstAvailableId={tenants[0]?.id}
          onSwitch={onPick}
          switching={switching}
        />
      ) : null}
      <div ref={containerRef} className="relative">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-label="Switch tenant"
          className="inline-flex items-center gap-2 rounded-md border bg-background px-3 py-1.5 text-sm font-medium hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        >
          <span className="max-w-[16rem] truncate">
            {currentTenant?.name || "Select tenant"}
          </span>
          <svg
            aria-hidden="true"
            width="14"
            height="14"
            viewBox="0 0 16 16"
            fill="none"
            className={open ? "rotate-180" : ""}
          >
            <path
              d="M4 6l4 4 4-4"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>
        {open ? (
          <ul
            role="listbox"
            aria-label="Available tenants"
            className="absolute right-0 z-40 mt-1 min-w-[16rem] rounded-md border bg-popover p-1 shadow-md"
          >
            {tenants.map((t) => (
              <li key={t.id} role="presentation">
                <button
                  type="button"
                  role="option"
                  aria-selected={t.current}
                  disabled={switching}
                  onClick={() => onPick(t.id)}
                  className={
                    "flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-sm hover:bg-accent disabled:cursor-not-allowed disabled:opacity-60 " +
                    (t.current ? "font-semibold" : "font-normal")
                  }
                >
                  <span className="truncate">{t.name || t.id}</span>
                  {t.current ? (
                    <span
                      aria-hidden="true"
                      className="text-xs text-muted-foreground"
                    >
                      current
                    </span>
                  ) : null}
                </button>
              </li>
            ))}
          </ul>
        ) : null}
      </div>
      {errorMsg ? (
        <p
          role="alert"
          className="max-w-md rounded-md border border-destructive bg-destructive/10 px-2 py-1 text-xs text-destructive"
        >
          {errorMsg}
        </p>
      ) : null}
    </div>
  );
}

// MembershipRemovedBanner surfaces the P0-192-7 banner when the
// current_tenant has been removed from the operator's available
// list. Renders a Switch-to-first-available default action plus a
// dismissable hint about the eviction-is-eventual contract
// (P0-192-8 — explicit, not apologetic).
function MembershipRemovedBanner({
  firstAvailableId,
  onSwitch,
  switching,
}: {
  firstAvailableId?: string;
  onSwitch: (id: string) => void | Promise<void>;
  switching: boolean;
}) {
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center gap-2 rounded-md border border-amber-500/40 bg-amber-50 px-3 py-1.5 text-xs text-amber-900 dark:bg-amber-900/20 dark:text-amber-100"
    >
      <span>
        Your access to the current tenant was removed. Switch to another tenant
        or sign out.
      </span>
      {firstAvailableId ? (
        <button
          type="button"
          disabled={switching}
          onClick={() => onSwitch(firstAvailableId)}
          className="rounded border border-amber-500/60 px-2 py-0.5 font-medium hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-60 dark:hover:bg-amber-900/40"
        >
          Switch tenant
        </button>
      ) : null}
    </div>
  );
}
