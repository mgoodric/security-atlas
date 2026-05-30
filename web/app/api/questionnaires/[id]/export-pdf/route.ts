// Slice 263 — BFF proxy for `POST /v1/questionnaires/{id}/export-pdf`.
//
// Streams the PDF bytes back to the browser unchanged. Unlike the other
// BFFs in this slice (which forward JSON), this route must preserve the
// `application/pdf` Content-Type + the raw byte stream so the browser
// triggers a file-save dialog.
//
// Wire shape:
//   POST /api/questionnaires/{id}/export-pdf
//   -> 200 application/pdf (PDF bytes) — or upstream's error JSON
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only. RLS enforces tenancy.
//   * P0-263-4: consumes the slice 155 ExportPDF handler verbatim.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function POST(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(id)}/export-pdf`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  // Preserve the upstream content-type so binary PDF bytes pass through
  // without re-encoding. Error responses are JSON; success is
  // application/pdf — the upstream sets the right header.
  const contentType =
    upstream.headers.get("Content-Type") ?? "application/octet-stream";
  const buf = await upstream.arrayBuffer();
  return new NextResponse(buf, {
    status: upstream.status,
    headers: { "Content-Type": contentType },
  });
}
