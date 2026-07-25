import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { mapClaimScfAnchor } from "@/lib/api/oscal-components";

// Slice 620 — server-side proxy for mapping an unmapped vendor claim to a
// canonical SCF anchor. The request body carries `{scf_anchor_id}` (the SCF
// code); it is forwarded to the upstream PATCH
// /v1/oscal/component-claims/{id}/scf-anchor. The platform validates the
// anchor against the bundled catalog and gates the write on the grc_engineer
// role; this proxy only injects the cookie bearer and shuttles the result.
export async function PATCH(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  let body: { scf_anchor_id?: string };
  try {
    body = (await req.json()) as { scf_anchor_id?: string };
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  const scfAnchorID = (body.scf_anchor_id ?? "").trim();
  if (!scfAnchorID) {
    return NextResponse.json(
      { error: "scf_anchor_id is required" },
      { status: 400 },
    );
  }
  try {
    const result = await mapClaimScfAnchor(bearer, id, scfAnchorID);
    return NextResponse.json(result);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
