import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { dispositionClaim, Disposition } from "@/lib/api/oscal-components";

// Server-side proxy for the operator disposition. The disposition verb rides
// in the request body (`{disposition, note}`) and is mapped to the upstream
// colon-verb route `POST /v1/oscal/component-claims/{id}:<verb>`.
const VALID: Disposition[] = ["accept", "reject", "needs-info"];

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  let body: { disposition?: string; note?: string };
  try {
    body = (await req.json()) as { disposition?: string; note?: string };
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  const disposition = body.disposition as Disposition;
  if (!VALID.includes(disposition)) {
    return NextResponse.json(
      { error: "disposition must be one of accept, reject, needs-info" },
      { status: 400 },
    );
  }
  try {
    const result = await dispositionClaim(
      bearer,
      id,
      disposition,
      body.note ?? "",
    );
    return NextResponse.json(result);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
