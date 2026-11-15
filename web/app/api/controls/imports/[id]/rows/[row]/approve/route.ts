// #1785 — BFF for POST /v1/controls/imports/{id}/rows/{row}/approve.
//
// Body: {"candidate_rank": n} or {"scf_id": "IAC-06"}, optionally with
// implementation_type and applicability_expr. The platform resolves the
// caller to the approving person and refuses a machine credential; a 409
// (duplicate organisation control ID, already decided) passes through.

import { NextResponse, type NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string; row: string }> },
) {
  const { id, row } = await ctx.params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json(
      { error: "body must be a JSON object" },
      { status: 400 },
    );
  }
  return forwardJSON(
    `/v1/controls/imports/${encodeURIComponent(id)}/rows/${encodeURIComponent(
      row,
    )}/approve`,
    { method: "POST", jsonBody: body },
  );
}
