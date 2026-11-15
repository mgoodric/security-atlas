// #2113 — /exceptions/[id] BFF: GET /v1/exceptions/{id}/audit-log proxy.
//
// The exception's lifecycle trail (request, approve, deny, activate,
// expire, revoke), read under the caller's bearer.

import { forwardJSON } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/exceptions/${encodeURIComponent(id)}/audit-log`);
}
