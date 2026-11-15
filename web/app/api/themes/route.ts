// #1818 — BFF proxy for the theme vocabulary.
//
//   GET  /api/themes -> GET  /v1/themes (built-ins + the organisation's own,
//                       each with its risk and rule usage)
//   POST /api/themes -> POST /v1/themes (add an organisation theme)
//
// Upstream status and body pass through verbatim; a caller without
// risk-taxonomy rights gets upstream's 403 on POST.

import { NextResponse } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function GET(): Promise<Response> {
  return noStore(await forwardJSON("/v1/themes"));
}

export async function POST(req: Request): Promise<Response> {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  return noStore(
    await forwardJSON("/v1/themes", { method: "POST", jsonBody: body }),
  );
}
