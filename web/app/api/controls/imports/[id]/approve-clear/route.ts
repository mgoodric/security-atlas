// #1785 — BFF for POST /v1/controls/imports/{id}/approve-clear. No body.
// A 409 while another approve-clear of the batch runs passes through.

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(`/v1/controls/imports/${id}/approve-clear`, {
    method: "POST",
  });
}
