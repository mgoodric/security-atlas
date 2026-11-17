// #2117 — /audits/[id] BFF: GET /v1/audit-periods/{id} proxy.
//
// Forwards the bearer cookie; the platform scopes the read to the
// caller's tenant via RLS (invariant #6). Upstream status and body pass
// through, so a 404 reaches the page as "not found". A non-UUID id is
// refused before it reaches the upstream path (#2312), and the response
// is never cached, so a period frozen in another tab doesn't show as open.

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return noStore(
    await forwardJSON(`/v1/audit-periods/${encodeURIComponent(id)}`),
  );
}
