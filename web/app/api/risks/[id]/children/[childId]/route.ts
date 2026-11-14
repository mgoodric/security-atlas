// #1748 — BFF proxy for detaching one child risk from its parent.
//
// DELETE /api/risks/{id}/children/{childId}
//   -> DELETE /v1/risks/{id}/children/{child_id}
//
// Upstream answers 204 on success and 404 when the child is not linked (or
// either risk is outside the session's tenant). Neither risk is deleted.

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function DELETE(
  _req: Request,
  ctx: { params: Promise<{ id: string; childId: string }> },
): Promise<Response> {
  const { id, childId } = await ctx.params;
  return noStore(
    await forwardJSON(
      `/v1/risks/${encodeURIComponent(id)}/children/${encodeURIComponent(
        childId,
      )}`,
      { method: "DELETE" },
    ),
  );
}
