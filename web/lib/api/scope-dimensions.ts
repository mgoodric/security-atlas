// #2103 — the tenant's scope dimensions, for the scope-dimension picker.
//
// `ScopeDimensionWire` mirrors `dimWire` in internal/api/scopes/handlers.go.

import { APIError } from "./base";
import { apiFetch } from "./_shared";
import type { ScopeDimensionWire } from "@/components/scope/scope-picker-model";

export type ScopeDimensionsListResponse = {
  dimensions: ScopeDimensionWire[];
};

// Server-side fn: the /api/scope-dimensions BFF route calls the platform
// with the caller's bearer.
export async function listScopeDimensions(
  bearer: string,
): Promise<ScopeDimensionWire[]> {
  const res = await apiFetch("/v1/scopes/dimensions", bearer);
  const body = (await res.json()) as ScopeDimensionsListResponse;
  return body.dimensions ?? [];
}

// Browser-side fn: the picker calls this via TanStack Query.
export async function fetchScopeDimensions(): Promise<ScopeDimensionsListResponse> {
  const res = await fetch("/api/scope-dimensions");
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as ScopeDimensionsListResponse;
}
