// Slice 471 — BFF proxy for the markdown export
// `GET /v1/controls/checklist/{generationId}/export.md` (AC-11).
//
// The platform renders ONLY the APPROVED sections and returns 422 when no
// section is approved (a draft checklist cannot be exported — P0-471-1). This
// BFF streams the markdown (or the upstream error JSON) back verbatim; it never
// re-decides the approval gate.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ generationId: string }> },
): Promise<Response> {
  const { generationId } = await params;
  const bad = rejectNonUUID({ generationId });
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/controls/checklist/${encodeURIComponent(
      generationId,
    )}/export.md`,
    { headers: { Authorization: `Bearer ${bearer}` }, cache: "no-store" },
  );
  const body = await upstream.text();
  // Preserve the upstream content-type: text/markdown on success, JSON on the
  // 422 "approve first" error.
  const contentType =
    upstream.headers.get("Content-Type") ?? "application/json";
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": contentType },
  });
}
