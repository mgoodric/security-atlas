// Slice 223 — BFF proxy for `/v1/search` (the slice 268 unified search
// endpoint).
//
// Forwards the SESSION_COOKIE bearer to the upstream platform; the
// bearer never reaches the browser. Mirrors the slice 102 audits BFF
// shape (`web/app/api/audits/route.ts`) so the BFF surface stays
// predictable across reads.
//
// AC-2 (slice 223): "Search BFF endpoint at web/app/api/search/route.ts
// forwards the bearer cookie to a new upstream `GET /v1/search?q=...`
// endpoint. The upstream endpoint queries SCF anchors + tenant
// controls + evidence + risks via existing sqlc query paths; RLS
// enforces tenant isolation at the database layer."
//
// The upstream is slice 268's `GET /v1/search` (merged on main at
// d9d8e69b). Wire shape:
//   IN:  ?q=<query>&types=controls,risks,evidence&limit=N
//   OUT: { hits: [...], count: N, partial_types: [...] }
//
// The BFF is a thin proxy — upstream owns validation (`q` length,
// `limit` cap, type whitelist) and the per-type OPA admit. Errors
// pass through verbatim so the FE can surface inline 400 copy without
// reinventing the contract.
//
// Constitutional invariants:
//   * Invariant 6 (tenant isolation): the bearer is forwarded; the
//     platform's `/v1/search` runs the per-type queries under the
//     tenant GUC via `tenancy.ApplyTenant`. The BFF never reads or
//     forwards a tenant_id (P0-223-1 — RLS bypass is impossible at
//     this layer).
//   * Invariant 5 (FrameworkScope intersection): inherited from the
//     upstream's per-type query paths — slice 268 reuses the existing
//     sqlc projections that already respect applicability + framework
//     scope.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(req: NextRequest): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  // Pass the request's search params through verbatim. URL parsing is
  // resilient to a missing query string (`new URL("/api/search",
  // "http://x").search` is "") so the upstream receives the empty
  // case unchanged and returns the 400 it owns.
  const incoming = new URL(req.url);
  const upstreamURL = `${apiBaseURL()}/v1/search${incoming.search}`;
  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
