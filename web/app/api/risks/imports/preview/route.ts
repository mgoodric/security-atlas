// #2848 — BFF for POST /v1/risks/imports/preview: the mapping step's view
// of an upload (header row, the field each header maps to, the fields the
// methodology reads, the first rows). Nothing is staged.

import type { NextRequest } from "next/server";

import { forwardRiskUpload } from "../upload-form";

export function POST(req: NextRequest) {
  return forwardRiskUpload(req, "/v1/risks/imports/preview");
}
