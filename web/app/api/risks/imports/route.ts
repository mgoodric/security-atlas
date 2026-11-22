// #2848 — BFF for POST /v1/risks/imports: stage a risk register upload with
// its column map (201 with the staged batch, 400/413 with the reason).

import type { NextRequest } from "next/server";

import { forwardRiskUpload } from "./upload-form";

export function POST(req: NextRequest) {
  return forwardRiskUpload(req, "/v1/risks/imports");
}
