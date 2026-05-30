// Slice 263 — BFF proxy for `PATCH /v1/questionnaires/{id}/answers/{qid}`.
//
// Single-answer upsert. Used by the right-pane answer editor's debounced
// autosave. The platform handler (slice 155 UpsertAnswer) takes a
// upsertAnswerRequest:
//
//   {
//     answer_value:   string,
//     narrative:      string,
//     citations:      [],
//     authored_by:    string,
//     save_to_library: bool,
//     scf_anchor_id:  string,
//     source_label:   string,
//   }
//
// The BFF forwards the JSON body verbatim — upstream owns validation +
// the actual upsert semantics. Operator's identity is derived from the
// bearer server-side (the `authored_by` field is informational).
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only. RLS enforces tenancy.
//   * P0-263-4: consumes the slice 155 UpsertAnswer handler verbatim.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ id: string; qid: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
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
    )}/answers/${encodeURIComponent(qid)}`,
    {
      method: "PATCH",
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
