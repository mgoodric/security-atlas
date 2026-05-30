import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";
import { getControlRisks } from "@/lib/api/control-detail";

// Slice 253 — server-side proxy for GET /v1/controls/{id}/risks
// (slice 064 control-detail handler — Risks). Renders the right-rail
// Risks treated card on the control-detail view. Reads the httpOnly
// bearer cookie and forwards it as Authorization on the upstream call.
// Pure read-only — no request body.

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
    const risks = await getControlRisks(bearer, id);
    return NextResponse.json(risks);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
