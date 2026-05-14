// Slice 032 — board-pack BFF: publish a draft pack.
//
//   POST /api/board-packs/{id}/publish -> POST /v1/board-packs/{id}/publish
//
// The platform rejects the publish with 409 unless every section is
// approved (decision D6) — the BFF forwards that status verbatim so the
// UI can surface the "not ready" reason.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

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
  return forwardJSON(`/v1/board-packs/${encodeURIComponent(id)}/publish`, {
    method: "POST",
    jsonBody: body,
  });
}
