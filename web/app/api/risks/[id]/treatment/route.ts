// #2105 — /risks/[id] BFF: PATCH /v1/risks/{id}/treatment.
//
// Changes a risk's treatment to untreated, mitigate, transfer or avoid.
// The upstream status and body pass through: 400 for a treatment the
// platform's validation refuses (mitigate with nothing linked), 409 while
// an acceptance is proposed or approved.

import { NextResponse } from "next/server";

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function PATCH(
  req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  return noStore(
    await forwardJSON(`/v1/risks/${encodeURIComponent(id)}/treatment`, {
      method: "PATCH",
      jsonBody: body,
    }),
  );
}
