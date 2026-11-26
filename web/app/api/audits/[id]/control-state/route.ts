// #2117 — /audits/[id] BFF: GET /v1/audit-periods/{id}/control-state
// proxy. The horizon read for one control: the evidence observed up to
// the period's frozen_at, or up to now while the period is open. Never
// cached, since the horizon moves when the period is frozen (#2312).

import { type NextRequest, NextResponse } from "next/server";

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const control = req.nextUrl.searchParams.get("control");
  if (!control) {
    return NextResponse.json(
      { error: "control query parameter is required" },
      { status: 400 },
    );
  }
  const qs = new URLSearchParams({ control });
  return noStore(
    await forwardJSON(
      `/v1/audit-periods/${encodeURIComponent(
        id,
      )}/control-state?${qs.toString()}`,
    ),
  );
}
