// #1785 — BFF for POST /v1/controls/imports/{id}/rows/{row}/approve.
//
// Body: {"candidate_rank": n} or {"scf_id": "IAC-06"}, optionally with
// implementation_type and applicability_expr. Only those four fields are
// forwarded. The platform resolves the caller to the approving person and
// refuses a machine credential; a 409 (duplicate organisation control ID,
// already decided) passes through.

import type { NextRequest } from "next/server";

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";
import { pickBody, readJSONObject } from "@/lib/api/control-imports-bff";

const FIELDS = [
  "candidate_rank",
  "scf_id",
  "implementation_type",
  "applicability_expr",
];

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string; row: string }> },
) {
  const { id, row } = await ctx.params;
  const bad = rejectNonUUID({ id, row });
  if (bad) return bad;
  const body = await readJSONObject(req);
  if (body instanceof Response) return body;
  return forwardJSON(`/v1/controls/imports/${id}/rows/${row}/approve`, {
    method: "POST",
    jsonBody: pickBody(body, FIELDS),
  });
}
