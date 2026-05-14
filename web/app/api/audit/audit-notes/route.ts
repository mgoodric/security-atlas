// Slice 042 — audit workspace BFF: POST /v1/audit-notes proxy (slice 029).
//
// Creates a note (auditor_only | shared) on a control / sample /
// walkthrough / finding / period anchor. The platform dispatches in-app
// notifications to prior-thread authors on `shared` notes. This BFF
// forwards the body verbatim — the upstream is the single source of
// truth for visibility + parent-note validation.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function POST(req: NextRequest) {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return forwardJSON("/v1/audit-notes", { method: "POST", jsonBody: body });
}
