import { NextRequest, NextResponse } from "next/server";

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

// #1794 — POST /v1/framework-scopes/{id}/applicability/bulk. One decision,
// justification and implementation reference on many requirements; all or
// nothing upstream.

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  return forwardJSON(
    `/v1/framework-scopes/${encodeURIComponent(id)}/applicability/bulk`,
    { method: "POST", jsonBody: body },
  );
}
