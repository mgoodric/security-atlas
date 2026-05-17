// Slice 072 — version metadata client.
//
// `useVersion()` is a thin TanStack Query hook over `/api/version` (the
// BFF route at web/app/api/version/route.ts which proxies the platform's
// public `GET /v1/version`). The upstream endpoint is intentionally
// public (no bearer required — it's metadata, not tenant data; see the
// handler comment in internal/api/version.go), and the BFF route stays
// public too so SSR and the login page can read the version before any
// authentication.
//
// Anti-criterion P0-A5 — over-fetching is the failure mode here, not
// stale data. Version does not change between binary restarts, so the
// hook uses aggressive caching: 24h staleTime + 7d gcTime. A typical
// session reads the version once and never refetches.

import { useQuery, type UseQueryResult } from "@tanstack/react-query";

// VersionInfo mirrors the four-field JSON shape returned by the platform.
// Reordering or renaming fields is a breaking change to the BFF + the
// VersionFooter component.
export type VersionInfo = {
  version: string;
  commit: string;
  build_time: string;
  go_version: string;
};

// Default placeholder rendered when the version is unknown (network
// failure or pre-fetch). The footer degrades gracefully to "v?" rather
// than blocking render.
export const UNKNOWN_VERSION: VersionInfo = {
  version: "?",
  commit: "",
  build_time: "",
  go_version: "",
};

const ONE_DAY_MS = 24 * 60 * 60 * 1000;
const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000;

// fetchVersion is the shared fetch path for both the hook (browser) and
// any SSR helper that wants to read the version server-side. It hits the
// same-origin BFF route (no upstream URL handling client-side).
export async function fetchVersion(): Promise<VersionInfo> {
  const res = await fetch("/api/version", { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`fetchVersion: status ${res.status}`);
  }
  return (await res.json()) as VersionInfo;
}

// useVersion returns the cached version. The hook is intentionally
// non-suspense — consumers (VersionFooter) check `isLoading` and render
// "v?" rather than blocking the page on a metadata fetch.
export function useVersion(): UseQueryResult<VersionInfo> {
  return useQuery<VersionInfo>({
    queryKey: ["version"],
    queryFn: fetchVersion,
    staleTime: ONE_DAY_MS,
    gcTime: SEVEN_DAYS_MS,
    // No automatic refetch on focus / reconnect / interval. The version
    // never changes for a running binary, so a single fetch per session
    // is correct (anti-criterion P0-A5 — no network round-trip on every
    // page load).
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchInterval: false,
    retry: false,
  });
}
