// Slice 263 — BFF proxy for `GET /v1/questionnaires/{id}`.
//
// Returns the questionnaire with its question + answer set (slice 155's
// getResponse shape: { questionnaire, questions }).
//
// Constitutional invariants:
//   * Invariant 6 (tenant isolation): the bearer is forwarded; the
//     platform handler runs under the tenant GUC.
//   * P0-263-4: this BFF consumes the slice 155 Get handler — no
//     backend changes.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(id)}`,
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
