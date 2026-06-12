// Slice 441 — BFF proxy for
// `POST /v1/questionnaires/{id}/answers/{qid}/ai-suggest`.
//
// Generates a cited AI DRAFT answer for one question. The platform handler
// (qaisuggest.Service) does keyword-first-pass retrieval, local-Ollama
// generation, and mandatory-citation enforcement; it returns one of three
// shapes (drafted / insufficient_evidence / suppressed). The draft is NEVER a
// customer-facing answer — it is persisted ai_assisted + unapproved until the
// operator approves it via the ai-approve route.
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only; RLS enforces tenancy.
//   * AI-assist boundary: the approver is derived server-side from the bearer;
//     this route never lets the client claim a draft is approved.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string; qid: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id, qid } = await params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(
      id,
    )}/answers/${encodeURIComponent(qid)}/ai-suggest`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
