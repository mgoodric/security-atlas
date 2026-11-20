// Slice 468 — browser-side client for the server-backed saved filter-views.
//
// This is the server-backed half of slice 448's SavedViewStore seam: the
// /controls page swaps its localStorage reads/writes for these fetches
// against the BFF (`/api/saved-views`), which proxies the RLS- + per-user-
// scoped `/v1/saved-views` upstream. The wire shape mirrors the upstream
// handler's savedViewWire (id, name, filters).
//
// The filter payload is a flat string map narrowed to the slice-224
// controls-filter keys; the upstream re-validates it (threat-model T), so
// this client does not need to (but the page's sanitizeFilters still runs
// on read for defense-in-depth).

import { APIError } from "./base";
import { upstreamErrorMessage } from "./_shared";

export type ServerSavedView = {
  id: string;
  name: string;
  filters: Record<string, string>;
};

/**
 * The list a view belongs to (#2133). The platform treats a request that
 * names none as `controls`.
 */
export type SavedViewSurface = "controls" | "risks";

// Maximum length of a user-supplied view name, on every surface. Names
// are display-only labels; the cap bounds the stored payload and the list
// rendering.
export const MAX_VIEW_NAME_LENGTH = 60;

type ListResponse = { views: ServerSavedView[] };

// bffJSON is a browser-side fetch against the BFF that unwraps an upstream
// `{error}` body into the thrown APIError message. Supports a method + JSON
// body (the GET-only bffControlFetch in _shared.ts does not).
async function bffJSON<T>(
  path: string,
  init?: { method?: string; body?: unknown },
): Promise<T> {
  const res = await fetch(path, {
    method: init?.method ?? "GET",
    headers:
      init?.body !== undefined
        ? { "Content-Type": "application/json" }
        : undefined,
    body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
  });
  if (!res.ok) {
    const msg = await upstreamErrorMessage(res);
    throw new APIError(res.status, msg);
  }
  // DELETE returns 204 with no body.
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** List the calling user's saved views for one list. */
export async function listSavedViews(
  surface: SavedViewSurface = "controls",
): Promise<ServerSavedView[]> {
  const out = await bffJSON<ListResponse>(
    `/api/saved-views?surface=${surface}`,
  );
  return out.views ?? [];
}

/** Create a saved view. Throws APIError(409) on a duplicate name. */
export async function createSavedView(
  name: string,
  filters: Record<string, string>,
  surface: SavedViewSurface = "controls",
): Promise<ServerSavedView> {
  return bffJSON<ServerSavedView>("/api/saved-views", {
    method: "POST",
    body: { name, filters, surface },
  });
}

/** Delete one of the caller's saved views by id. */
export async function deleteSavedView(id: string): Promise<void> {
  await bffJSON<void>(`/api/saved-views/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
