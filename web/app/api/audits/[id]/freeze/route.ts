// #2117 — /audits/[id] BFF: POST /v1/audit-periods/{id}/freeze proxy.
//
// Freezing is irreversible (invariant #10). The platform owns every
// refusal: 403 for a role it does not admit, 409 on a re-freeze. This
// BFF forwards and passes the status through.

import { forwardJSON } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/audit-periods/${encodeURIComponent(id)}/freeze`, {
    method: "POST",
  });
}
