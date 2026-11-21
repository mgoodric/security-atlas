// #2847 — BFF for POST /v1/risks/imports/{id}/rows/{row}/approve: approve
// one staged row. No body. A 409 (already decided) and a 422 (the risk
// store refused the row's values) pass through.

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string; row: string }> },
) {
  const { id, row } = await ctx.params;
  const bad = rejectNonUUID({ id, row });
  if (bad) return bad;
  return forwardJSON(`/v1/risks/imports/${id}/rows/${row}/approve`, {
    method: "POST",
  });
}
