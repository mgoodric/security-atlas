// #2117 — /audits/[id] BFF: GET /v1/audit-periods/{id} proxy.
//
// Forwards the bearer cookie; the platform scopes the read to the
// caller's tenant via RLS (invariant #6). Upstream status and body pass
// through, so a 404 reaches the page as "not found".

import { forwardJSON } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/audit-periods/${encodeURIComponent(id)}`);
}
