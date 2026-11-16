// #2118 — /audits/[id] BFF: remove one auditor from the period.
//
//   DELETE -> DELETE /v1/audit-periods/{id}/auditors/{userId}
//
// The platform answers 204 on removal, 404 when the user held no grant,
// 403 for a non-admin. Both ids are checked first: encodeURIComponent
// leaves `..` intact (security-atlas#2312).

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function DELETE(
  _req: Request,
  ctx: { params: Promise<{ id: string; userId: string }> },
) {
  const { id, userId } = await ctx.params;
  const bad = rejectNonUUID({ id, userId });
  if (bad) return bad;
  return forwardJSON(
    `/v1/audit-periods/${encodeURIComponent(id)}/auditors/${encodeURIComponent(
      userId,
    )}`,
    { method: "DELETE" },
  );
}
