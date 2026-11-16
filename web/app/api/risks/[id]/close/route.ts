// #2107 — /risks/[id] BFF: PATCH /v1/risks/{id}/close.
//
// Closes an open risk with a reason (not_applicable | merged |
// realised_and_handled) and an optional note. The upstream status and body
// pass through: 400 for a missing or unknown reason, 409 when the risk is
// already closed.

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
    await forwardJSON(`/v1/risks/${encodeURIComponent(id)}/close`, {
      method: "PATCH",
      jsonBody: body,
    }),
  );
}
