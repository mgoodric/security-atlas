// Slice 032 — board-pack BFF: edit one section of a draft pack.
//
//   PUT /api/board-packs/{id}/sections/{key}
//     -> PUT /v1/board-packs/{id}/sections/{key}
//
// Forwards the operator edit (override_text and/or structured inputs)
// verbatim. The platform rejects an edit of a published pack with 409
// (AC-7 immutability) and an unknown section key with 404 — the BFF
// passes both statuses through.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function PUT(
  req: NextRequest,
  ctx: { params: Promise<{ id: string; key: string }> },
) {
  const { id, key } = await ctx.params;
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return forwardJSON(
    `/v1/board-packs/${encodeURIComponent(id)}/sections/${encodeURIComponent(
      key,
    )}`,
    { method: "PUT", jsonBody: body },
  );
}
