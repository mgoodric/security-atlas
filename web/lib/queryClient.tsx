// SSR-aware QueryClient factory per the TanStack Query v5 App-Router guide.
// One instance per server request; one shared singleton in the browser.
//
// See https://tanstack.com/query/latest/docs/framework/react/guides/advanced-ssr
import { QueryClient } from "@tanstack/react-query";

import { isFeatureDisabledError } from "@/lib/feature-nav";
import { tenantGuardedMutationCache } from "@/lib/tenant-cache";

// shouldRetryQuery retries a failed query once, except a gated module's
// 404 "feature disabled": that is the platform's final answer, and
// retrying it kept the page on its loading-state form for a second before
// it swapped to the disabled panel, unmounting whatever held focus
// (#2266).
export function shouldRetryQuery(failureCount: number, error: unknown) {
  if (isFeatureDisabledError(error)) return false;
  return failureCount < 1;
}

function makeQueryClient() {
  return new QueryClient({
    // #2715: a mutation in flight across a tenant switch does not write.
    mutationCache: tenantGuardedMutationCache(),
    defaultOptions: {
      queries: {
        staleTime: 60 * 1000,
        retry: shouldRetryQuery,
      },
    },
  });
}

let browserQueryClient: QueryClient | undefined;

export function getQueryClient() {
  if (typeof window === "undefined") {
    return makeQueryClient();
  }
  browserQueryClient ??= makeQueryClient();
  return browserQueryClient;
}
