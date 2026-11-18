// #2135 — BFF proxy for GET /v1/needs-attention (#2134).
//
// Reads the bearer cookie server-side and forwards only the parameters
// the platform reads (for, type, limit, offset). Ranking, the view's
// authz (for=all is admins and GRC leads) and module filtering are the
// platform's; this route passes its answer, or its error status, through.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import {
  getNeedsAttention,
  needsAttentionQuery,
  type NeedsAttentionView,
} from "@/lib/api/needs-attention";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(request: Request) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const sp = new URL(request.url).searchParams;
  const limit = sp.get("limit");
  const offset = sp.get("offset");
  const type = sp.get("type");
  const search = needsAttentionQuery({
    view: (sp.get("for") as NeedsAttentionView | null) ?? undefined,
    types: type ? type.split(",") : undefined,
    limit: limit != null ? Number(limit) : undefined,
    offset: offset != null ? Number(offset) : undefined,
  });
  try {
    return NextResponse.json(await getNeedsAttention(bearer, search));
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
