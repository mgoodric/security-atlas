// #2085 — the browser cache of the tenant's people directory, the one
// <Person> resolves IDs against.
//
// The browser QueryClient is a singleton that outlives a tenant switch
// (the switcher only calls router.refresh()) and a sign-out (a server
// action that redirects in place). A directory cached for one tenant must
// never resolve names on another, so it is dropped on every tenant
// switch, in this tab or a sibling's (the slice-199 broadcast), and on
// sign-out. Within one tenant, it is fresh for one page visit.

import type { QueryClient } from "@tanstack/react-query";

import { onTenantSwitched } from "@/lib/auth/tenant-broadcast";

export const PEOPLE_QUERY_KEY = ["people"] as const;

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
