// Slice 042 — audit workspace BFF: POST /v1/walkthroughs proxy (slice 027).
//
// Creates a walkthrough record (status=draft). NOTE: slice 027's handler
// gates writes on IsAdmin OR grc_engineer. Canvas §8.3 says a walkthrough
// is an "auditor OR owner" recorded explanation — so a pure `auditor`
// credential may receive a 403 here. That is a backend role gap, NOT a
// frontend bug: this BFF forwards the upstream 403 verbatim and the
// WalkthroughRecorder component surfaces it as a clear message. The gap
// is surfaced to the orchestrator as a follow-up backend slice.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function POST(req: NextRequest) {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return forwardJSON("/v1/walkthroughs", { method: "POST", jsonBody: body });
}
