// Slice 536b — BFF for one crosswalk edge's audit trail.
//
// GET /api/admin/crosswalk-edges/{id}/audit
//   -> GET /v1/admin/crosswalk-edges/{id}/audit
//
// Returns both trails for the edge: the slice-536b content edits and the
// slice-483 tier transitions. This is the in-product proof that no edit and no
// approval went unrecorded, which is why the review UI links to it from every
// row.
//
// Admin-scoped upstream: editor / reviewer identity appears in this payload and
// never on the public /anchors surface (the slice-483 P0-483-6 boundary). The
// BFF replicates none of that gate — the platform is the authority — it only
// declines to forward a malformed id.
//
// no-store: the trail changes on every edit the operator makes in the same
// session, so a browser-cached copy would show a reviewer their own edit as
// missing from the audit log — the one reading this surface exists to refute.

import { forwardJSON, noStore } from "@/lib/api/bff";

const UUID_RE =
  /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export async function GET(
  _request: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  if (!UUID_RE.test(id)) {
    return Response.json({ error: "invalid edge id" }, { status: 400 });
  }
  return noStore(
    await forwardJSON(
      `/v1/admin/crosswalk-edges/${encodeURIComponent(id)}/audit`,
    ),
  );
}
