import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";
import { getControlPolicies } from "@/lib/api/control-detail";

// Slice 253 — server-side proxy for GET /v1/controls/{id}/policies
// (slice 064 control-detail handler — Policies). Renders the right-rail
// Policies card on the control-detail view. Reads the httpOnly bearer
// cookie and forwards it as Authorization on the upstream call so the
// token never reaches the browser. Pure read-only — no request body.
//
// Mirrors the shape of the existing per-control proxies
// (`coverage/route.ts`, `state/route.ts`, `effectiveness/route.ts`):
// 401 when the bearer cookie is absent; upstream status + message
// passed through verbatim otherwise.

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
    const policies = await getControlPolicies(bearer, id);
    return NextResponse.json(policies);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
