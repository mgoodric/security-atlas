// Slice 757 — BFF proxy for
// `POST /v1/questionnaires/{id}/answers/{qid}/ai-reject`.
//
// Discards an UNAPPROVED AI draft: the question returns to unanswered and the
// platform audit-logs the rejection with the draft's model provenance. The
// platform refuses approved or manual targets with 409 (P0-757-4) and absent /
// cross-tenant ids with 404 (RLS-invisible). One request rejects exactly one
// answer — there is no bulk variant of this route.
//
// Body forwarded verbatim: { answer_id }.
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only; RLS enforces tenancy.
//   * AI-assist boundary: rejection (like approval) is a per-answer operator
//     action; the actor is derived from the bearer server-side.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string; qid: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id, qid } = await params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(
      id,
    )}/answers/${encodeURIComponent(qid)}/ai-reject`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
