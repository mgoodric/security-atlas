import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";
import { getControlEffectiveness } from "@/lib/api";

// Slice 041 — server-side proxy for GET /v1/controls/{id}/effectiveness
// (slice 012 control state evaluation). Drives the effectiveness KPI
// card: the rolling 30-day pass rate. Reads the httpOnly bearer cookie
// and forwards it as Authorization.

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  try {
    const effectiveness = await getControlEffectiveness(bearer, id);
    return NextResponse.json(effectiveness);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
