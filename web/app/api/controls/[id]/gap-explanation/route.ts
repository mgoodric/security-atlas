import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getControlGapExplanation } from "@/lib/api/control-detail";

// Slice 444 — server-side proxy for GET /v1/controls/{id}/gap-explanation
// (slice 444 AI gap-explanation v0). Drives the Overview-tab gap-explanation
// card: the deterministic freshness rollup plus a NON-BINDING, cited,
// local-Ollama explanation when one is available. Reads the httpOnly bearer
// cookie and forwards it as Authorization so the token never reaches the
// browser. Pure read-only — no request body, no approve/publish/export path
// (P0-444-3). Mirrors the shape of the existing per-control proxies
// (policies/route.ts, state/route.ts): 401 when the bearer cookie is absent;
// upstream status + message passed through verbatim otherwise.

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
    const body = await getControlGapExplanation(bearer, id);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
