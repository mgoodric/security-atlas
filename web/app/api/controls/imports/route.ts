// #1785 — BFF for POST /v1/controls/imports: the control sheet upload.
// The body (file, framework, optional repeated column_map and
// ignore_column) is forwarded as sent; see ./upload-form.ts.

import type { NextRequest } from "next/server";

import { forwardControlUpload } from "./upload-form";

export function POST(req: NextRequest) {
  return forwardControlUpload(req, "/v1/controls/imports");
}
