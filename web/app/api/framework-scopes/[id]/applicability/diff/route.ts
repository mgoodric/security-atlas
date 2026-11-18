import { NextRequest } from "next/server";

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

// #1795 — GET /v1/framework-scopes/{id}/applicability/diff. The decisions
// and predicate that changed against the activated revision, for the
// approver of a revision in review.

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return noStore(
    await forwardJSON(
      `/v1/framework-scopes/${encodeURIComponent(id)}/applicability/diff`,
    ),
  );
}
