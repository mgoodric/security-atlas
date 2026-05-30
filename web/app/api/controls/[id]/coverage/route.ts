import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getControlCoverage } from "@/lib/api/control-detail";

// Slice 041 — server-side proxy for GET /v1/controls/{id}/coverage
// (slice 008 UCF graph traversal). Reads the httpOnly bearer cookie and
// forwards it as Authorization on the upstream call so the token never
// reaches the browser. Pure read-only — no request body.

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  try {
    const coverage = await getControlCoverage(bearer, id);
    return NextResponse.json(coverage);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
