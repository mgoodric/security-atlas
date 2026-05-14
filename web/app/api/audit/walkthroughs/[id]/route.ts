// Slice 042 — audit workspace BFF: GET /v1/walkthroughs/{id} proxy (slice 027).
// Returns the walkthrough with attachments + canonical_hash + tamper flag.

import { forwardJSON } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/walkthroughs/${encodeURIComponent(id)}`);
}
