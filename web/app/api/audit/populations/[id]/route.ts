// Slice 042 — audit workspace BFF: GET /v1/populations/{id} proxy (slice 026).

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(`/v1/populations/${encodeURIComponent(id)}`);
}
