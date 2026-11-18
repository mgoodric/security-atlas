import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getControlState } from "@/lib/api/control-detail";
import { rejectNonUUID } from "@/lib/api/bff";

// Slice 041 — server-side proxy for GET /v1/controls/{id}/state
// (slice 012 control state evaluation). Drives the freshness clock:
// freshness_status, last_observed_at, freshness_class per scope cell.
// Reads the httpOnly bearer cookie and forwards it as Authorization.

export async function GET(
  _req: NextRequest,
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
  try {
    const state = await getControlState(bearer, id);
    return NextResponse.json(state);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
