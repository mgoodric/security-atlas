// Slice 032 — board-pack BFF: approve one section of a draft pack.
//
//   POST /api/board-packs/{id}/sections/{key}/approve
//     -> POST /v1/board-packs/{id}/sections/{key}/approve
//
// Sets the per-section approval flag. The publish gate (decision D6)
// requires every section approved. Approving a section on a published
// pack returns 409 — forwarded verbatim.

import { forwardJSON } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string; key: string }> },
) {
  const { id, key } = await ctx.params;
  return forwardJSON(
    `/v1/board-packs/${encodeURIComponent(id)}/sections/${encodeURIComponent(
      key,
    )}/approve`,
    { method: "POST", jsonBody: {} },
  );
}
