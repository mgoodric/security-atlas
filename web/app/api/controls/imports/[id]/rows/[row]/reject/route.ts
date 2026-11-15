// #1785 — BFF for POST /v1/controls/imports/{id}/rows/{row}/reject.
// Body: {"reason": "..."}; the platform requires a non-blank reason.

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
    )}/reject`,
    { method: "POST", jsonBody: body },
  );
}
