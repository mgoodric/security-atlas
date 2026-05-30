import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getControlHistory } from "@/lib/api/control-detail";

// Slice 253 — server-side proxy for GET /v1/controls/{id}/history
// (slice 064 control-detail handler — History). Renders the right-rail
// Audit log card on the control-detail view. Reads the httpOnly bearer
// cookie and forwards it as Authorization on the upstream call. Pure
// read-only — no request body. Pagination is server-side via the
// upstream's `?cursor=`/`?limit=` keyset; the right-rail card renders
// the default page (newest first) only — deeper trails belong to the
// dedicated History tab/page (a follow-on slice).

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
    const history = await getControlHistory(bearer, id);
    return NextResponse.json(history);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
