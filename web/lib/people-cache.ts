// #2085 — the browser cache of the tenant's people directory, the one
// <Person> resolves IDs against.
//
// The browser QueryClient is a singleton that outlives a tenant switch
// (the switcher only calls router.refresh()) and a sign-out (a server
// action that redirects in place). A directory cached for one tenant must
// never resolve names on another, so it is dropped on every tenant
// switch, in this tab or a sibling's (the slice-199 broadcast), and on
// sign-out. Within one tenant, it is fresh for one page visit.
//
// #2399: the directory is also keyed by the session's tenant, so a path
// that drops nothing (session expiry, then signing in again in the same
// tab) still never reads one tenant's names under another. With the
// tenant unknown the query does not run, and <Person> shows loading.

import type { QueryClient } from "@tanstack/react-query";

import { onTenantSwitched } from "@/lib/auth/tenant-broadcast";

// The prefix every directory entry sits under, whatever its tenant.
export const PEOPLE_QUERY_KEY = ["people"] as const;

/**
 * The directory query for one tenant, optionally narrowed to an optional
 * module's members (#2174). `enabled` is false while the tenant is
 * unknown: no entry is read or fetched without a tenant in its key.
 */
export function peopleDirectoryQuery(
  tenantId: string | undefined,
  module?: string,
) {
  const tenantKey = [...PEOPLE_QUERY_KEY, tenantId ?? ""];
  return {
    queryKey: module ? [...tenantKey, { module }] : tenantKey,
    enabled: tenantId !== undefined && tenantId !== "",
  };
}

// One page visit: every <Person> on a page shares one fetch; the next
// visit after this window fetches again.
export const PEOPLE_STALE_MS = 30_000;

/** Drops the cached directory, so the next read fetches it again. */
export function forgetPeopleDirectory(qc: QueryClient): void {
  qc.removeQueries({ queryKey: PEOPLE_QUERY_KEY });
}

/**
 * Drops the cached directory whenever any tab, this one included,
 * broadcasts a tenant switch. Returns the unsubscribe.
 */
export function forgetPeopleOnTenantSwitch(qc: QueryClient): () => void {
  return onTenantSwitched(() => forgetPeopleDirectory(qc));
}
