// #1785 — BFF for POST /v1/controls/imports: the control sheet upload.
//
// The multipart body (file, framework, optional repeated column_map) is
// re-posed verbatim; the platform's size, row and column caps and its
// header detection are the single source of truth. Status and body pass
// through (201 with the staged batch, 400/413 with the reason).

import type { NextRequest } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";

export async function POST(req: NextRequest) {
  return forwardMultipart("/v1/controls/imports", await req.formData());
}
