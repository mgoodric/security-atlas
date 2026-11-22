// #2848 — the shared body of the risk import upload and preview BFFs.
//
// The multipart body (file, methodology, repeated column_map and
// ignore_column) is re-posted verbatim; the platform's size, row and column
// caps and its header detection are the single source of truth. Status and
// body pass through.
//
// readBoundedUpload (#2344) checks the bearer, the declared and streamed
// length against the platform's cap, and multipart/form-data before the
// form is parsed.

import { type NextRequest, NextResponse } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";
import { parseBoundedForm, readBoundedUpload } from "@/lib/api/upload";

// MAX_UPLOAD_BYTES mirrors riskimport.MaxUploadBytes (5 MiB) plus the
// handler's multipartOverhead (64 KiB) in internal/api/risks/riskimports.
const MAX_UPLOAD_BYTES = (5 << 20) + (64 << 10);

const NOT_MULTIPART = "request must be multipart/form-data with a file field";

export async function forwardRiskUpload(
  req: NextRequest,
  upstreamPath: string,
): Promise<NextResponse> {
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
  return forwardMultipart(upstreamPath, form);
}
