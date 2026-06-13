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

import { forwardJSON } from "@/lib/api/bff";

export async function GET(): Promise<Response> {
  return forwardJSON("/v1/saved-views");
}

export async function POST(req: NextRequest): Promise<Response> {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return forwardJSON("/v1/saved-views", { method: "POST", jsonBody: body });
}
