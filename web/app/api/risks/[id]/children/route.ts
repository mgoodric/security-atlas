// #1748 — BFF proxy for a risk's children (the risk detail page's Children
// section).
//
// GET  /api/risks/{id}/children -> GET  /v1/risks/{id}/children
// POST /api/risks/{id}/children -> POST /v1/risks/{id}/children
//                                  body { child_risk_ids: string[] }
//
// The bearer stays server-side and no tenant_id is forwarded: the platform
// scopes both calls to the cookie session's tenant (RLS, invariant 6), so a
// foreign risk id resolves to a clean upstream 404. Upstream status + body
// pass through verbatim, including the 400 (level) and 409 (cycle)
// refusals, so the page can show the platform's message.

import { forwardJSON, noStore } from "@/lib/api/bff";

function upstreamPath(id: string): string {
  return `/v1/risks/${encodeURIComponent(id)}/children`;
}

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  // The listing changes on every attach / detach; never browser-cache it.
  return noStore(await forwardJSON(upstreamPath(id)));
}

export async function POST(
  req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return Response.json({ error: "invalid JSON body" }, { status: 400 });
  }
  return noStore(
    await forwardJSON(upstreamPath(id), { method: "POST", jsonBody: body }),
  );
}
