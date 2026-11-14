// #1748 — BFF proxy for the parent risks a risk rolls up to (the risk
// detail page's Parents section).
//
// GET /api/risks/{id}/parents -> GET /v1/risks/{id}/parents

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  return noStore(
    await forwardJSON(`/v1/risks/${encodeURIComponent(id)}/parents`),
  );
}
