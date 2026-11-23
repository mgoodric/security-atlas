// #3002 — BFF for POST /v1/risks/imports/{id}/close: close a staged import
// nobody will finish, rejecting its pending rows. Body: {"reason": "..."};
// only `reason` is forwarded, and the platform requires it to be non-blank.

import type { NextRequest } from "next/server";

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";
import { pickBody, readJSONObject } from "@/lib/api/control-imports-bff";

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const body = await readJSONObject(req);
  if (body instanceof Response) return body;
  return forwardJSON(`/v1/risks/imports/${id}/close`, {
    method: "POST",
    jsonBody: pickBody(body, ["reason"]),
  });
}
