// #2107 — /risks/[id] BFF: PATCH /v1/risks/{id}/reopen.
//
// Reopens a closed risk and clears its closure. It takes no input; the
// upstream status and body pass through, 409 when the risk is open.

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function PATCH(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return noStore(
    await forwardJSON(`/v1/risks/${encodeURIComponent(id)}/reopen`, {
      method: "PATCH",
      jsonBody: {},
    }),
  );
}
