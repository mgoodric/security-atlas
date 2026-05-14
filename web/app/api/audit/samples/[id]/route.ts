// Slice 042 — audit workspace BFF: GET /v1/samples/{id} proxy (slice 026).
// Returns the sample with its evidence_record_ids list.

import { forwardJSON } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/samples/${encodeURIComponent(id)}`);
}
