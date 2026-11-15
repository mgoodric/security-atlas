// #1785 — BFF for GET /v1/controls/imports/{id}: the batch with its
// counts, staged rows and rows refused at upload.

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return noStore(await forwardJSON(`/v1/controls/imports/${id}`));
}
