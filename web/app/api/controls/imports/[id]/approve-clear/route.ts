// #1785 — BFF for POST /v1/controls/imports/{id}/approve-clear. No body.
// A 409 while another approve-clear of the batch runs passes through.

import { forwardJSON } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(
    `/v1/controls/imports/${encodeURIComponent(id)}/approve-clear`,
    { method: "POST" },
  );
}
