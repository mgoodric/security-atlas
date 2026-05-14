// Slice 042 — audit workspace BFF: POST /v1/populations proxy (slice 026).
//
// Creates a sample-pull population for a control. The platform enforces
// the frozen-evidence horizon (observed_at <= COALESCE(frozen_at,
// 'infinity')) at the query layer — invariant 10 is honored server-side;
// this BFF just forwards the body verbatim.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function POST(req: NextRequest) {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return forwardJSON("/v1/populations", { method: "POST", jsonBody: {} });
  }
  return forwardJSON("/v1/populations", { method: "POST", jsonBody: body });
}
