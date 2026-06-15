import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getPeriodEvidenceSummary } from "@/lib/api/control-detail";

// Slice 749 — server-side proxy for
// GET /v1/audit-periods/{id}/controls/{controlID}/evidence-summary (the
// period-scoped, FROZEN-population AI evidence summary; the audit-workspace
// sibling of slice 502's live control-detail summary). Drives the
// audit-workspace evidence-summary card: the deterministic bounded
// FROZEN-population evidence set (observed_at <= frozen_at — invariant #10) plus
// a NON-BINDING, cited, local-default-Ollama summary when one is available. Reads
// the httpOnly bearer cookie and forwards it as Authorization so the token never
// reaches the browser. Pure read-only — no request body, no
// approve/publish/export path (P0-502-3). Mirrors the shape of the slice-502
// per-control proxy: 401 when the bearer cookie is absent; upstream status +
// message passed through verbatim otherwise (so a 409 "not frozen" surfaces).

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string; controlId: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id, controlId } = await ctx.params;
  try {
    const body = await getPeriodEvidenceSummary(bearer, id, controlId);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
