// #2106 — BFF proxy for unlinking one control from a risk.
//
// DELETE /api/risks/{id}/controls/{controlId}
//   -> DELETE /v1/risks/{id}/controls/{control_id}
//
// Upstream answers 200 with the risk on success; 404 when the control is
// not linked (or either id is outside the session's tenant); 409 when the
// risk is closed; 400 when it is the last control of a mitigate risk with
// no live action plan. Status + body pass through so the page shows the
// platform's message.

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function DELETE(
  _req: Request,
  ctx: { params: Promise<{ id: string; controlId: string }> },
): Promise<Response> {
  const { id, controlId } = await ctx.params;
  const bad = rejectNonUUID({ id, control_id: controlId });
  if (bad) return bad;
  return noStore(
    await forwardJSON(
      `/v1/risks/${encodeURIComponent(id)}/controls/${encodeURIComponent(
        controlId,
      )}`,
      { method: "DELETE" },
    ),
  );
}
