// Slice 468 — BFF proxy for deleting one of the caller's saved views.
//
// DELETE /api/saved-views/{id} -> DELETE /v1/saved-views/{id}
//
// The upstream scopes the delete to the caller's user_id (sourced from the
// verified credential, never the path), so a foreign id resolves to 404 —
// a caller can never delete another user's view (P0-448-5).

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function DELETE(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  // Mutating response for per-user mutable state — never browser-cache it
  // (companion to the GET no-store fix; slice 746).
  return noStore(
    await forwardJSON(`/v1/saved-views/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  );
}
