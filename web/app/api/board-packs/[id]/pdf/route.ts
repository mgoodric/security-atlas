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
// 503 from upstream (Chrome unavailable on the server) is forwarded as a
// JSON error so the UI can surface the reason — the existing detail page
// pattern.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
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
    return new NextResponse(text, {
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
