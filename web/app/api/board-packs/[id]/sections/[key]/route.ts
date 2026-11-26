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

import { forwardJSON, rejectNonUUID, rejectUnsafeSegment } from "@/lib/api/bff";

const SECTION_KEYS: ReadonlySet<string> = new Set([
  "posture",
  "top_risks",
  "coverage_trend",
  "open_findings",
  "vendor_burndown",
  "operational_metrics",
  "investment",
  "asks",
]);

export async function PUT(
  req: NextRequest,
  ctx: { params: Promise<{ id: string; key: string }> },
) {
  const { id, key } = await ctx.params;
  const bad =
    rejectNonUUID({ id }) ?? rejectUnsafeSegment({ key }, SECTION_KEYS);
  if (bad) return bad;
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
