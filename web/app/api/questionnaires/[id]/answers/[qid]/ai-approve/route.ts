// Slice 441 — BFF proxy for
// `POST /v1/questionnaires/{id}/answers/{qid}/ai-approve`.
//
// One-click human approval of an AI-suggested draft. The platform handler
// records the approver (derived from the bearer server-side — NEVER
// client-supplied) and flips human_approved=TRUE; the operator's edited final
// text is what the questionnaire stores. The DB CHECK makes human_approved
// without a human_approver impossible (P0-441-8). There is no auto-approve
// path: this route is the only way an AI draft becomes approved.
//
// Body forwarded verbatim: { answer_id, narrative, answer_value }.
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only; RLS enforces tenancy.
//   * AI-assist boundary: approval requires the human action this route gates.

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
    )}/answers/${encodeURIComponent(qid)}/ai-approve`,
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
