// Slice 032 — board-pack BFF: fetch one pack by id.
//
//   GET /api/board-packs/{id} -> GET /v1/board-packs/{id}
//
// Returns the pack content (draft or published) verbatim from the platform.

import { forwardJSON } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/board-packs/${encodeURIComponent(id)}`);
}
