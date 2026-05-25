// Slice 263 — BFF proxy for `GET /v1/questionnaires/{id}/suggestions`.
//
// Forwards the `?anchor=<scf_id>` query param to the platform's
// deterministic suggestion lookup (slice 155 D2; D7 of slice 263).
//
// CRITICAL — AI-assist boundary (P0-263-1):
//   This endpoint is NOT an LLM call. The platform's
//   SuggestForAnchorWithPool is a deterministic pattern-match: most-
//   recent-N prior library entries for the given SCF anchor. The
//   suggestions panel UI consumes this verbatim. There is ZERO model
//   inference happening at any layer of this path. The frontend MUST
//   NOT style the panel as "AI suggestions" — see suggestions-panel.tsx
//   for the enforcement (no model badges, no confidence numbers).
//
// Wire shape:
//   GET /api/questionnaires/{id}/suggestions?anchor=IAC-06
//   -> { suggestions: Suggestion[] }
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only. RLS enforces tenancy.
//   * P0-263-4: consumes the slice 155 Suggestions handler verbatim.
//   * P0-263-7: only the slice 155 + slice 268 endpoints are reachable
//     through this slice's BFFs. This route hits slice 155's
//     /v1/questionnaires/{id}/suggestions.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  const anchor = req.nextUrl.searchParams.get("anchor") ?? "";
  // Empty anchor is forwarded so the upstream returns its canonical
  // 400 response — the BFF does not invent validation copy (the same
  // discipline as slice 223's /api/search BFF).
  const qs = new URLSearchParams({ anchor });
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(
      id,
    )}/suggestions?${qs.toString()}`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
