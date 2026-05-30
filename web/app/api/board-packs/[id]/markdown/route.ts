// Slice 043 — board-pack BFF: Markdown export (binary-safe text passthrough).
//
//   GET /api/board-packs/{id}/markdown -> GET /v1/board-packs/{id}.md
//
// Why a dedicated route (not forwardJSON): the platform returns
// text/markdown with a Content-Disposition: attachment header. forwardJSON
// re-encodes the body as JSON, which would corrupt the download. This
// route streams the text/markdown bytes verbatim and preserves the
// Content-Disposition so the browser drops a file named
// board-pack-{period}.md.
//
// The Next.js dynamic-route segment ".md" cannot appear directly in a
// folder name on every filesystem (and conflicts with the existing
// catch-all id route), so we expose this under the cleaner /markdown
// suffix and translate to the platform's `{id}.md` literal upstream.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/board-packs/${encodeURIComponent(id)}.md`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  if (!upstream.ok) {
    // Platform error envelope is JSON; forward verbatim with its status
    // so the UI can surface 404 / 401 / 5xx the same way it does for
    // the other board-pack routes.
    const text = await upstream.text();
    return new NextResponse(text, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  }
  const text = await upstream.text();
  const disposition =
    upstream.headers.get("Content-Disposition") ??
    `attachment; filename="board-pack-${id}.md"`;
  return new NextResponse(text, {
    status: 200,
    headers: {
      "Content-Type": "text/markdown; charset=utf-8",
      "Content-Disposition": disposition,
    },
  });
}
