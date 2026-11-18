// Slice 042 — audit workspace BFF: GET /v1/walkthroughs/{id} proxy (slice 027).
// Returns the walkthrough with attachments + canonical_hash + tamper flag.

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(`/v1/walkthroughs/${encodeURIComponent(id)}`);
}
