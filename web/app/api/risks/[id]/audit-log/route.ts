// #2104 — BFF proxy for the risk page's History section: forwards to
// `GET /v1/risks/{id}/audit-log` with the cookie bearer. The upstream is
// tenant-scoped by RLS; this route never takes a tenant from the client.

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  // The history grows on every edit; never browser-cache it.
  return noStore(
    await forwardJSON(`/v1/risks/${encodeURIComponent(id)}/audit-log`),
  );
}
