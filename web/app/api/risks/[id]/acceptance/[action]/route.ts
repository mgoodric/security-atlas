// #2105 — /risks/[id] BFF: PATCH /v1/risks/{id}/acceptance/{action}.
//
// One proxy for the four acceptance transitions: propose (justification
// and proposed expiry), approve (accepted_until), reject (optional reason)
// and revoke (reason). Any other segment is refused here with 404 and
// never reaches the platform. The platform owns every other refusal: 400
// for input, 403 for the approver role or the approval rule, 409 for a
// wrong state.

import { NextResponse } from "next/server";

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

const ALLOWED = new Set(["propose", "approve", "reject", "revoke"]);

export async function PATCH(
  req: Request,
  ctx: { params: Promise<{ id: string; action: string }> },
) {
  const { id, action } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  if (!ALLOWED.has(action)) {
    return NextResponse.json({ error: "unknown transition" }, { status: 404 });
  }
  let body: unknown = {};
  const text = await req.text();
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
    }
  }
  return noStore(
    await forwardJSON(
      `/v1/risks/${encodeURIComponent(id)}/acceptance/${action}`,
      { method: "PATCH", jsonBody: body },
    ),
  );
}
