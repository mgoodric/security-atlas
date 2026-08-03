// Slice 757 — BFF proxy for `POST /v1/questionnaires/{id}/answer-runs`.
//
// Starts a slice-756 batch answer-drafting run. Execution is request-scoped
// and sequential on the platform side (756 D1), so this request returns the
// COMPLETED run detail (status + per-item outcomes); a second start while one
// is active is a platform 409. The run only ever persists UNAPPROVED drafts —
// review + per-answer approval happen in the slice-757 queue.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(id)}/answer-runs`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: "{}",
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
