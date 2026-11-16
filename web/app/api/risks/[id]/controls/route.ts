// #2106 — BFF proxy for linking a control to a risk from the risk page.
//
// POST /api/risks/{id}/controls -> POST /v1/risks/{id}/controls
//                                  body { control_id }
//
// Both ids must be UUIDs before anything goes upstream. No tenant_id is
// forwarded: the platform resolves the risk and the control in the cookie
// session's tenant (RLS, invariant 6), so another tenant's control is a
// clean upstream 404. Upstream status + body pass through, including the
// 409 for a closed risk.

import { NextResponse } from "next/server";

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function POST(
  req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  const controlID =
    body && typeof body === "object"
      ? (body as { control_id?: unknown }).control_id
      : undefined;
  const bad = rejectNonUUID({
    id,
    control_id: typeof controlID === "string" ? controlID : "",
  });
  if (bad) return bad;
  return noStore(
    await forwardJSON(`/v1/risks/${encodeURIComponent(id)}/controls`, {
      method: "POST",
      jsonBody: { control_id: controlID },
    }),
  );
}
