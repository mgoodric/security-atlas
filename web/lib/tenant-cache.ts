// #2621 — no browser query outlives a tenant switch.
//
// The browser QueryClient is a singleton that survives a tenant switch:
// the switcher only swaps the JWT cookie and calls router.refresh(). Only
// the people directory carries the tenant in its key (#2399); the other
// query keys do not, so without this their old-tenant data would render
// under the new tenant until staleTime or gcTime ran out.
//
// resetQueries() puts every cached query back to its initial state, so a
// mounted view shows its loading state rather than the old tenant's data,
// and refetches the active ones under the new cookie. Inactive queries
// refetch when next mounted. It is a superset of forgetPeopleDirectory().
//
// Keying every query by tenant instead (a key factory or queryKeyHashFn)
// was not taken: the browser does not know its tenant id until
// /api/me/tenants answers, so the hash would need a second source of
// truth for it. A reset on the one path that changes the tenant is
// enough; sign-out and re-login are covered by <ForgetPreviousSession>.

import type { QueryClient } from "@tanstack/react-query";

/** Resets every cached query, so nothing the old tenant fetched renders. */
export function forgetTenantCaches(qc: QueryClient): void {
  void qc.resetQueries();
}
