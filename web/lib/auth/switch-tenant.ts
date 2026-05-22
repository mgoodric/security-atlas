// switch-tenant.ts — slice 192 client-side tenant switcher.
//
// Drives the frontend half of the multi-tenant switch flow. The
// `<TenantSwitcher>` component (web/components/auth/tenant-switcher.tsx)
// invokes `switchTenant(targetTenantId)` when the operator picks a
// non-current tenant from the dropdown.
//
// The function calls the BFF route `/api/auth/switch-tenant` which
// in turn invokes the platform's `POST /oauth/token` with
// `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`
// (slice 188 primitive). On success the BFF replaces the
// atlas_jwt cookie with the new token; the function then calls
// `router.refresh()` so server components re-render against the
// new tenant scope.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-192-3: the frontend uses the OAuth token-exchange RFC as
//     the contract. NEVER bypasses via a custom session-mutate
//     endpoint. The BFF route is a pure proxy + cookie-swapper —
//     it does not mint, parse, or rewrite the JWT itself.
//   - P0-192-6: this slice does NOT modify slice 188's
//     /oauth/token handler. The frontend is a pure consumer.
//   - P0-192-9: no per-tenant URL routing — the new JWT carries
//     the tenant scope, the URL stays the same.

export type SwitchTenantResult =
  | { ok: true }
  | { ok: false; status: number; message: string };

// switchTenant calls the BFF route to swap the operator's
// current_tenant via the OAuth token-exchange grant. Returns the
// outcome; the caller is responsible for calling `router.refresh()`
// on success.
//
// The function does NOT throw on non-2xx responses — instead it
// returns a structured `ok: false` so the component can render the
// appropriate error UI without a try/catch wrapping every call site.
export async function switchTenant(
  targetTenantId: string,
): Promise<SwitchTenantResult> {
  if (!targetTenantId) {
    return {
      ok: false,
      status: 400,
      message: "target tenant id is required",
    };
  }
  let resp: Response;
  try {
    resp = await fetch("/api/auth/switch-tenant", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ target_tenant_id: targetTenantId }),
      credentials: "include",
    });
  } catch (e) {
    return {
      ok: false,
      status: 0,
      message: e instanceof Error ? e.message : "network error",
    };
  }
  if (!resp.ok) {
    let message = `tenant switch failed (status ${resp.status})`;
    try {
      const body = await resp.text();
      if (body) {
        message = body.length > 280 ? body.slice(0, 280) + "..." : body;
      }
    } catch {
      // ignore — keep the status-only message
    }
    return { ok: false, status: resp.status, message };
  }
  return { ok: true };
}
