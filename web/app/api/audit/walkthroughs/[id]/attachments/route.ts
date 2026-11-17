// Slice 042 — audit workspace BFF: walkthrough attachment upload proxy.
//
//   POST /v1/walkthroughs/{id}/attachments   multipart/form-data
//
// Streams the FormData (file + optional annotations JSON) through to the
// platform, which persists the blob to the slice-036 artifact store and
// recomputes the walkthrough canonical_hash. Same auth gate as creation
// applies upstream (IsAdmin OR grc_engineer) — 403 is forwarded verbatim.
// readBoundedUpload (#2344) caps the body before the form is parsed.

import { NextRequest, NextResponse } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";
import { parseBoundedForm, readBoundedUpload } from "@/lib/api/upload";

// MAX_UPLOAD_BYTES mirrors the handler's MaxBytesReader in
// internal/api/walkthroughs: MaxAttachmentBytes (artifact.MaxUploadBytes,
// 100 MiB) plus 1 KiB for the multipart envelope.
const MAX_UPLOAD_BYTES = (100 << 20) + 1024;

const NOT_MULTIPART = "invalid multipart body";

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const read = await readBoundedUpload(req, {
    maxBytes: MAX_UPLOAD_BYTES,
    tooLarge: "attachment exceeds the 100 MiB upload cap",
    notMultipart: NOT_MULTIPART,
  });
  if (!read.ok) return read.response;
  const form = await parseBoundedForm(read.upload);
  if (!form) {
    return NextResponse.json({ error: NOT_MULTIPART }, { status: 400 });
  }
  return forwardMultipart(
    `/v1/walkthroughs/${encodeURIComponent(id)}/attachments`,
    form,
  );
}
