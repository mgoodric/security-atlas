// Slice 042 — audit workspace BFF: GET /v1/populations/{id} proxy (slice 026).

import { forwardJSON } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/populations/${encodeURIComponent(id)}`);
}
