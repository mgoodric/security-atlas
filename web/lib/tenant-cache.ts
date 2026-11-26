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
//
// #2715 — a mutation in flight across the switch is the other way old
// data gets back in: its onSuccess runs after the reset and writes the
// old tenant's response into a key the new tenant's view reads (e.g.
// ["risks", "review-cadence"]). Clearing the mutation cache does not stop
// it; query-core calls options.onSuccess whether or not the mutation is
// still cached. tenantGuardedMutationCache() drops that onSuccess instead.
// See docs/audit-log/2715-tenant-switch-mutation-guard-decisions.md.
//
// #2768 — a handler that writes the cache after a plain awaited request
// is not a mutation, so the guard above never sees it. It reads
// currentTenantEpoch() before the request and skips the write (and any
// navigation to the result) if the epoch moved while it waited.

import {
  MutationCache,
  type Mutation,
  type QueryClient,
} from "@tanstack/react-query";

// The tenant epoch of each guarded cache: bumped by every reset.
const epochs = new WeakMap<MutationCache, number>();
// The epoch each mutation started in.
const startedIn = new WeakMap<Mutation<unknown, unknown, unknown>, number>();

/** Resets every cached query, so nothing the old tenant fetched renders. */
export function forgetTenantCaches(qc: QueryClient): void {
  const mc = qc.getMutationCache();
  epochs.set(mc, (epochs.get(mc) ?? 0) + 1);
  void qc.resetQueries();
}

/**
 * The tenant epoch of qc, which forgetTenantCaches() bumps. Read it before
 * an awaited request and compare after: if it changed, the tenant switched
 * while the request was in flight, the response is the old tenant's, and
 * it must not be written into the cache. A QueryClient whose cache is not
 * tenantGuardedMutationCache() still counts its resets.
 */
export function currentTenantEpoch(qc: QueryClient): number {
  return epochs.get(qc.getMutationCache()) ?? 0;
}

/**
 * The browser QueryClient's MutationCache. A mutation that started before
 * the last forgetTenantCaches() keeps its result and its onSettled, but
 * its onSuccess does not run, so it cannot write the old tenant's response
 * into the cache.
 *
 * Callbacks passed to mutate(vars, { onSuccess }) live on the observer,
 * which query-core keeps private, and are NOT stopped. Write to the cache
 * from useMutation's own onSuccess (tenant-switch-guard.test.ts flags a
 * setQueryData in the other form).
 */
export function tenantGuardedMutationCache(): MutationCache {
  const cache: MutationCache = new MutationCache({
    onMutate: (_variables, mutation) => {
      startedIn.set(mutation, epochs.get(cache) ?? 0);
    },
    onSuccess: (_data, _variables, _context, mutation) => {
      // Read from @tanstack/query-core 5.100.10, Mutation.execute: it
      // awaits this config.onSuccess, THEN reads this.options.onSuccess,
      // so replacing the options here is what stops the stale write. The
      // late-write test in tenant-cache.test.ts runs the real
      // QueryClient and fails if that ordering changes.
      //
      // A mutation with no stamp (restored from a persister, which this
      // app does not use) counts as stale: fail closed.
      if (startedIn.get(mutation) !== (epochs.get(cache) ?? 0)) {
        mutation.setOptions({ ...mutation.options, onSuccess: undefined });
      }
    },
  });
  return cache;
}
