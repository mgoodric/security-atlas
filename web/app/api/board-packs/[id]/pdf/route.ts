// Slice 043 — board-pack BFF: PDF export (binary-safe byte passthrough).
//
//   GET /api/board-packs/{id}/pdf -> GET /v1/board-packs/{id}/pdf
//
// Why a dedicated route (not forwardJSON): the platform returns
// application/pdf bytes. forwardJSON re-encodes the body as JSON, which
// would corrupt the PDF. This route reads the upstream as an ArrayBuffer
// and streams the bytes verbatim, preserving the Content-Disposition
// attachment header so the browser drops a file named
// board-pack-{period}.pdf.
//
// A non-2xx from upstream is forwarded as a JSON error so the UI can surface
// the reason — the existing detail page pattern. No Chrome on the server is a
// 409 feature_not_enabled whose message passes through; a busy or timed-out
// render is a 503 with Retry-After (#2786).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { rejectNonUUID, upstreamBody } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/board-packs/${encodeURIComponent(id)}/pdf`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  if (!upstream.ok) {
    const text = await upstream.text();
    return new NextResponse(upstreamBody(upstream.status, text), {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  }
  const bytes = await upstream.arrayBuffer();
  const disposition =
    upstream.headers.get("Content-Disposition") ??
    `attachment; filename="board-pack-${id}.pdf"`;
  return new NextResponse(bytes, {
    status: 200,
    headers: {
      "Content-Type": "application/pdf",
      "Content-Disposition": disposition,
    },
  });
}
