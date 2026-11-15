// #1785 — BFF for POST /v1/controls/imports/{id}/rows/{row}/reject.
// Body: {"reason": "..."}; only `reason` is forwarded, and the platform
// requires it to be non-blank.

import type { NextRequest } from "next/server";

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";
import { pickBody, readJSONObject } from "@/lib/api/control-imports-bff";

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string; row: string }> },
) {
  const { id, row } = await ctx.params;
  const bad = rejectNonUUID({ id, row });
  if (bad) return bad;
  const body = await readJSONObject(req);
  if (body instanceof Response) return body;
  return forwardJSON(`/v1/controls/imports/${id}/rows/${row}/reject`, {
    method: "POST",
    jsonBody: pickBody(body, ["reason"]),
  });
}
