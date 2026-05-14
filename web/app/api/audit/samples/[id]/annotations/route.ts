// Slice 042 — audit workspace BFF: sample annotations proxy (slice 026).
//
//   GET  /v1/samples/{id}/annotations   list annotations on a sample
//   POST /v1/samples/{id}/annotations   annotate one evidence record
//
// The platform validates result ∈ {passed, failed, not-applicable}; this
// BFF forwards verbatim.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return forwardJSON(`/v1/samples/${encodeURIComponent(id)}/annotations`);
}

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return forwardJSON(
    `/v1/samples/${encodeURIComponent(id)}/annotations`,
    { method: "POST", jsonBody: body },
  );
}
