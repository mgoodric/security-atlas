// #2110 — BFF for POST /v1/controls:upload-bundle: Upload new version on
// control detail.
//
// The multipart body (one bundle.tar.gz file part) is re-posed verbatim to
// the existing bundle upload; its admin gate, parser, test gate and
// supersession are the single source of truth. Status and body pass
// through (200/201 with the upload result, 400 with the reason, 403 for a
// non-admin).
//
// readBoundedUpload (#2344) checks the bearer, the declared and streamed
// length against the platform's cap, and multipart/form-data before the
// form is parsed.

import { type NextRequest, NextResponse } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";
import { parseBoundedForm, readBoundedUpload } from "@/lib/api/upload";

// MAX_UPLOAD_BYTES mirrors the upload handler's maxRequestBytes (8 MiB) in
// internal/api/controls.
const MAX_UPLOAD_BYTES = 8 << 20;

const NOT_MULTIPART =
  "request must be multipart/form-data with a bundle.tar.gz file";

export async function POST(req: NextRequest) {
  const read = await readBoundedUpload(req, {
    maxBytes: MAX_UPLOAD_BYTES,
    tooLarge: "bundle exceeds the 8 MiB upload cap",
    notMultipart: NOT_MULTIPART,
  });
  if (!read.ok) return read.response;
  const form = await parseBoundedForm(read.upload);
  if (!form) {
    return NextResponse.json({ error: NOT_MULTIPART }, { status: 400 });
  }
  return forwardMultipart("/v1/controls:upload-bundle", form);
}
