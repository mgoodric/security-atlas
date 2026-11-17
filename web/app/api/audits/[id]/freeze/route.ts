// #2117 — /audits/[id] BFF: POST /v1/audit-periods/{id}/freeze proxy.
//
// Freezing is irreversible (invariant #10). The platform owns every
// refusal: 403 for a role it does not admit, 409 on a re-freeze. This
// BFF forwards and passes the status through, after refusing a non-UUID
// id: `..` would otherwise normalise upstream to `POST /v1/freeze` (#2312).

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(`/v1/audit-periods/${encodeURIComponent(id)}/freeze`, {
    method: "POST",
  });
}
