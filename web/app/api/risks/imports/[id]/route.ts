// #2847 — BFF for GET /v1/risks/imports/{id}: the risk import batch with
// its counts, unmapped columns, staged rows and rows refused at upload.

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return noStore(await forwardJSON(`/v1/risks/imports/${id}`));
}
