// #1785 — BFF for POST /v1/controls/imports: the control sheet upload.
//
// The multipart body (file, framework, optional repeated column_map) is
// re-posed verbatim; the platform's size, row and column caps and its
// header detection are the single source of truth. Status and body pass
// through (201 with the staged batch, 400/413 with the reason).
//
// readBoundedUpload (#2344) checks the bearer, the declared and streamed
// length against the platform's cap, and multipart/form-data before the
// form is parsed.

import { type NextRequest, NextResponse } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";
import { parseBoundedForm, readBoundedUpload } from "@/lib/api/upload";

// MAX_UPLOAD_BYTES mirrors controlimport.MaxUploadBytes (5 MiB) plus the
// handler's multipartOverhead (64 KiB) in internal/api/controlimports.
const MAX_UPLOAD_BYTES = (5 << 20) + (64 << 10);

const NOT_MULTIPART = "request must be multipart/form-data with a file field";

export async function POST(req: NextRequest) {
  const read = await readBoundedUpload(req, {
    maxBytes: MAX_UPLOAD_BYTES,
    tooLarge: "upload exceeds the 5 MiB size cap; split it into smaller files",
    notMultipart: NOT_MULTIPART,
  });
  if (!read.ok) return read.response;
  const form = await parseBoundedForm(read.upload);
  if (!form) {
    return NextResponse.json({ error: NOT_MULTIPART }, { status: 400 });
  }
  return forwardMultipart("/v1/controls/imports", form);
}
