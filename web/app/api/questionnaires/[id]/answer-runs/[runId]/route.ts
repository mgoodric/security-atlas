// Slice 757 — BFF proxy for `GET /v1/questionnaires/{id}/answer-runs/{runId}`.
//
// Reads one slice-756 run's status + per-item outcomes (fixed vocabulary:
// drafted / insufficient_evidence / suppressed / skipped_needs_mapping /
// skipped_already_answered / error). The review queue uses this to render run
// progress and the non-draft outcome work lists; reason codes are fixed and
// never carry model/backend detail (slice-367 leak discipline).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ id: string; runId: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id, runId } = await params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(
      id,
    )}/answer-runs/${encodeURIComponent(runId)}`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
