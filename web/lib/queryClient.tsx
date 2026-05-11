// SSR-aware QueryClient factory per the TanStack Query v5 App-Router guide.
// One instance per server request; one shared singleton in the browser.
//
// See https://tanstack.com/query/latest/docs/framework/react/guides/advanced-ssr
import { QueryClient } from "@tanstack/react-query";

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 60 * 1000,
        retry: 1,
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
