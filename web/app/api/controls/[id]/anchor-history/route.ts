import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getControlAnchorHistory } from "@/lib/api/control-detail";
import { rejectNonUUID } from "@/lib/api/bff";

// #2392 — server-side proxy for GET /v1/controls/{id}/anchor-history, the
// SCF remap decisions on the control's bundle, for the History tab. Reads
// the httpOnly bearer cookie and forwards it as Authorization; the
// platform's status (404 for another tenant's control) passes through.
// ?cursor= (a next_cursor the platform returned) is forwarded; the platform
// refuses a malformed one with a 400.

export async function GET(
  req: NextRequest,
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
    const cursor = new URL(req.url).searchParams.get("cursor") ?? undefined;
    return NextResponse.json(await getControlAnchorHistory(bearer, id, cursor));
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
