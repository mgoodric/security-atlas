// Slice 440 — BFF proxy for `POST /v1/board/narrative/generate`.
//
// Generates a cited, numeric-verified, shape-and-tone-enforced DRAFT of the
// control-coverage-summary board-narrative section. The platform handler
// (boardnarrative.Service) assembles the deterministic rollup, runs one
// local-Ollama generation, and enforces the four guardrail gates (citation /
// numeric / shape / tone) BEFORE returning a draft; it returns one of two
// shapes (drafted / suppressed). The draft is NEVER a board-binding artifact —
// it is persisted ai_assisted + unapproved until the operator approves it via
// the approve route.
//
// Body forwarded verbatim: { period_end }.
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only; RLS enforces tenancy.
//   * AI-assist boundary: the approver is derived server-side from the bearer
//     at approval time; this route never lets the client claim a draft is
//     approved.

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
  const upstream = await fetch(`${apiBaseURL()}/v1/board/narrative/generate`, {
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
