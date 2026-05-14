// Slice 042 — audit workspace BFF: POST /v1/samples proxy (slice 026).
//
// Draws a deterministic sample (n, seed) from a population. The platform
// validates n > 0 and a non-empty seed; this BFF forwards verbatim so the
// upstream validator stays the single source of truth.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function POST(req: NextRequest) {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return forwardJSON("/v1/samples", { method: "POST", jsonBody: {} });
  }
  return forwardJSON("/v1/samples", { method: "POST", jsonBody: body });
}
