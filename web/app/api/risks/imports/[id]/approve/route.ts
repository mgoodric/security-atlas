// #2847 — BFF for POST /v1/risks/imports/{id}/approve: approve every
// pending row. No body. The platform resolves the caller to the approving
// person and refuses a machine credential.

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(`/v1/risks/imports/${id}/approve`, { method: "POST" });
}
