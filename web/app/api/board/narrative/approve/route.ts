// Slice 440 — BFF proxy for `POST /v1/board/narrative/approve`.
//
// One-click human approval of an AI-drafted board-narrative section
// (guardrail 2 — per section). The platform handler records the approver
// (derived from the bearer server-side — NEVER client-supplied) and flips
// human_approved=TRUE; the operator's edited final text is what ships into the
// board pack. The DB CHECK makes human_approved without a human_approver
// impossible (P0-440-2). There is no auto-approve path: this route is the only
// way an AI-drafted section becomes approved.
//
// Body forwarded verbatim: { record_id, final_text }.
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only; RLS enforces tenancy.
//   * AI-assist boundary: approval requires the human action this route gates.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(req: NextRequest): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/board/narrative/approve`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
