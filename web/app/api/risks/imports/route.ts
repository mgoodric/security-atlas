// #2848 — BFF for POST /v1/risks/imports: stage a risk register upload with
// its column map (201 with the staged batch, 400/413 with the reason).
// #3002 — GET lists the organisation's newest imports with their pending
// and waiting counts.

import type { NextRequest } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";

import { forwardRiskUpload } from "./upload-form";

export async function GET() {
  return noStore(await forwardJSON("/v1/risks/imports"));
}

export function POST(req: NextRequest) {
  return forwardRiskUpload(req, "/v1/risks/imports");
}
