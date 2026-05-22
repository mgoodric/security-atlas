// /oauth/select-tenant — slice 192 multi-tenant login picker.
//
// Operators with ≥2 tenants land here after OIDC sign-in (the
// `prompt=tenant` extension of slice 189's /oauth/authorize flow).
// The page renders the operator's available tenants and lets them
// pick one. The pick calls switchTenant() — the same token-exchange
// path used by the persistent header dropdown — and then redirects
// to /dashboard.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-192-5: the tenant list is sourced from /api/me/tenants,
//     which is bounded to the JWT's atlas:available_tenants[] (a
//     claim populated from the user_roles join — slice 192's
//     user_resolver expansion). The picker NEVER shows tenants the
//     operator doesn't have access to.
//   - P0-192-3: pick → switchTenant() → /oauth/token via the BFF —
//     never a custom session-mutate endpoint.
//   - P0-192-9: no per-tenant URL routing. After pick, redirect to
//     /dashboard; the JWT cookie carries the new tenant scope.
//   - D3 (decisions log): the picker only renders when the operator
//     actually has ≥2 tenants. Single-tenant operators are
//     redirected straight to /dashboard. The `prompt=tenant` flag
//     that triggers this page is sent only-when-≥2 — the page is
//     defensive against the single-tenant case for the
//     direct-link scenario.

"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense } from "react";

import { switchTenant } from "@/lib/auth/switch-tenant";

interface Tenant {
  id: string;
  name: string;
  current: boolean;
}

interface TenantsResponse {
  tenants: Tenant[];
}

export default function SelectTenantPage() {
  return (
    <Suspense fallback={null}>
      <SelectTenantInner />
    </Suspense>
  );
}

function SelectTenantInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const returnTo = searchParams?.get("return_to") || "/dashboard";

  const [tenants, setTenants] = useState<Tenant[] | null>(null);
  const [pickingId, setPickingId] = useState<string | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const resp = await fetch("/api/me/tenants", {
          cache: "no-store",
          credentials: "include",
        });
        if (cancelled) return;
        if (!resp.ok) {
          setErrorMsg(`Failed to load tenants (status ${resp.status})`);
          setTenants([]);
          return;
        }
        const data = (await resp.json()) as TenantsResponse;
        const list = Array.isArray(data?.tenants) ? data.tenants : [];
        setTenants(list);
        // Single-tenant defensive redirect (D3): if the operator
        // arrives here with only one tenant, skip the picker.
        if (list.length === 1) {
          router.replace(returnTo);
          return;
        }
        // No-tenant case (operator was removed from all tenants
        // between login and picker render): show a helpful page.
        // No redirect — let the operator see the explanation.
      } catch (e) {
        if (cancelled) return;
        setErrorMsg(e instanceof Error ? e.message : "network error");
        setTenants([]);
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, [returnTo, router]);

  const onPick = useCallback(
    async (id: string) => {
      if (pickingId) return;
      setErrorMsg(null);
      setPickingId(id);
      try {
        const current = tenants?.find((t) => t.current);
        if (current && current.id === id) {
          router.replace(returnTo);
          return;
        }
        const result = await switchTenant(id);
        if (!result.ok) {
          setErrorMsg(result.message);
          setPickingId(null);
          return;
        }
        router.replace(returnTo);
      } catch (e) {
        setErrorMsg(e instanceof Error ? e.message : "switch failed");
        setPickingId(null);
      }
    },
    [pickingId, tenants, router, returnTo],
  );

  const list = useMemo(() => tenants ?? [], [tenants]);

  if (tenants === null) {
    return (
      <main className="mx-auto max-w-md p-8">
        <p className="text-sm text-muted-foreground">Loading tenants…</p>
      </main>
    );
  }

  if (list.length === 0) {
    return (
      <main className="mx-auto max-w-md p-8">
        <h1 className="mb-4 text-xl font-semibold">No tenant access</h1>
        <p className="mb-4 text-sm">
          Your account is not currently associated with any tenant. Contact your
          administrator to be added to a tenant.
        </p>
        {errorMsg ? (
          <p
            role="alert"
            className="rounded-md border border-destructive bg-destructive/10 p-2 text-xs text-destructive"
          >
            {errorMsg}
          </p>
        ) : null}
      </main>
    );
  }

  return (
    <main className="mx-auto max-w-md p-8">
      <h1 className="mb-2 text-xl font-semibold">Select tenant</h1>
      <p className="mb-6 text-sm text-muted-foreground">
        You have access to multiple tenants. Choose one to continue.
      </p>
      <ul className="space-y-2">
        {list.map((t) => (
          <li key={t.id}>
            <button
              type="button"
              disabled={pickingId !== null}
              onClick={() => onPick(t.id)}
              className="flex w-full items-center justify-between rounded-md border bg-background px-4 py-3 text-left text-sm hover:bg-accent disabled:cursor-not-allowed disabled:opacity-60"
            >
              <span className="truncate font-medium">{t.name || t.id}</span>
              <span className="ml-3 text-xs text-muted-foreground">
                {t.current
                  ? "current"
                  : pickingId === t.id
                    ? "switching…"
                    : "select"}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {errorMsg ? (
        <p
          role="alert"
          className="mt-4 rounded-md border border-destructive bg-destructive/10 p-2 text-xs text-destructive"
        >
          {errorMsg}
        </p>
      ) : null}
    </main>
  );
}
