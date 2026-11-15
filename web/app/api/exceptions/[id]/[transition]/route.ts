// #2113 — /exceptions/[id] BFF: PATCH /v1/exceptions/{id}/{transition}.
//
// One proxy for the four lifecycle actions a person takes: approve, deny
// (with a reason), activate (with an effective-from) and revoke (with a
// reason). Any other segment is refused here with 404 and never reaches
// the platform. The platform owns every other refusal: 403 for a role or
// the approval rule, 409 for a wrong state.

import { NextResponse } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

const ALLOWED = new Set(["approve", "deny", "activate", "revoke"]);

export async function PATCH(
  req: Request,
  ctx: { params: Promise<{ id: string; transition: string }> },
) {
  const { id, transition } = await ctx.params;
  if (!ALLOWED.has(transition)) {
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
  return forwardJSON(`/v1/exceptions/${encodeURIComponent(id)}/${transition}`, {
    method: "PATCH",
    jsonBody: body,
  });
}
