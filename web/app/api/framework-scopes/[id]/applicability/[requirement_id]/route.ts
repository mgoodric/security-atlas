import { NextRequest, NextResponse } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

// #1794 — PUT /v1/framework-scopes/{id}/applicability/{requirement_id}.
// Sets one decision. The justification-required and frozen-revision rules
// are the platform's; their error bodies pass through.

export async function PUT(
  req: NextRequest,
  ctx: { params: Promise<{ id: string; requirement_id: string }> },
) {
  const { id, requirement_id } = await ctx.params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  return forwardJSON(
    `/v1/framework-scopes/${encodeURIComponent(
      id,
    )}/applicability/${encodeURIComponent(requirement_id)}`,
    { method: "PUT", jsonBody: body },
  );
}
