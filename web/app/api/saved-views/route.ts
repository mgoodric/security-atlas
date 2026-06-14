// Slice 468 — BFF proxy for per-user saved filter-views (controls list).
//
// GET  /api/saved-views  -> GET  /v1/saved-views   (the caller's own views)
// POST /api/saved-views  -> POST /v1/saved-views   (create a view)
//
// The bearer cookie is read server-side by forwardJSON and never reaches
// the browser; the upstream's RLS (tenant) + per-user query predicate are
// the real isolation boundary (P0-448-5). The body shape is forwarded
// verbatim so the upstream's filter-payload validation (threat-model T) is
// the single source of truth.

import { NextRequest } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";

// Saved views are per-(tenant, user) mutable state — they must never be
// served from the browser HTTP cache. Without `no-store`, after a DELETE the
// React-Query refetch of GET /v1/saved-views can be answered from the
// browser cache with the pre-delete body, leaving the deleted view stuck
// in the controls `<select>` until a hard reload (slice 746 / slice 448
// AC-5). We attach the header PER ROUTE via the opt-in `noStore` wrapper
// rather than inside the shared `forwardJSON` helper, which many
// cache-friendly GET routes rely on — widening its blast radius is a P0
// anti-criterion (slice 746).

export async function GET(): Promise<Response> {
  return noStore(await forwardJSON("/v1/saved-views"));
}

export async function POST(req: NextRequest): Promise<Response> {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return noStore(
    await forwardJSON("/v1/saved-views", { method: "POST", jsonBody: body }),
  );
}
