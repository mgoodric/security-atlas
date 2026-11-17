// #2110 — BFF for POST /v1/controls/{id}/evaluate: Evaluate now.
//
// The platform runs evaluation for the control synchronously and returns
// its new state in the GET /v1/controls/{id}/state shape. Who may evaluate
// (the control's owner or an admin) is the platform's check; status and
// body pass through (200 with the state, 403 for anyone else).

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

export async function POST(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return forwardJSON(`/v1/controls/${id}/evaluate`, { method: "POST" });
}
