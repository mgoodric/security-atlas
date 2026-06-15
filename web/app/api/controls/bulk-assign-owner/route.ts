// Slice 468 — BFF proxy for the bulk owner-assign action.
//
// POST /api/controls/bulk-assign-owner -> POST /v1/controls:bulk-assign-owner
//
// The route segment cannot carry the upstream's `:bulk-assign-owner` colon
// verb (Next.js path segments), so the BFF uses a hyphen segment and maps to
// the colon-suffixed upstream path. The body ({owner_user_id, control_ids})
// forwards verbatim; the upstream re-checks role + tenant PER ITEM (the
// authz amplifier, AC-11) and caps the set server-side — the client cap is
// ergonomics only.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function POST(req: NextRequest): Promise<Response> {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return forwardJSON("/v1/controls:bulk-assign-owner", {
    method: "POST",
    jsonBody: body,
  });
}
