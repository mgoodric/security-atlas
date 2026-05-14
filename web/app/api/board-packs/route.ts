// Slice 032 — board-pack BFF: list + generate.
//
//   GET  /api/board-packs   -> GET  /v1/board-packs   (every pack for the tenant)
//   POST /api/board-packs   -> POST /v1/board-packs   (generate a draft pack)
//
// The bearer cookie never reaches the browser — forwardJSON reads the
// httpOnly cookie server-side and adds the Authorization header. The
// platform derives the tenant from the calling credential.

import { NextRequest } from "next/server";

import { forwardJSON } from "@/lib/api/bff";

export async function GET() {
  return forwardJSON("/v1/board-packs");
}

export async function POST(req: NextRequest) {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = {};
  }
  return forwardJSON("/v1/board-packs", { method: "POST", jsonBody: body });
}
