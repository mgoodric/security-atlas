// Slice 042 — audit workspace BFF: walkthrough attachment upload proxy.
//
//   POST /v1/walkthroughs/{id}/attachments   multipart/form-data
//
// Streams the multipart body (file + optional annotations JSON) through to
// the platform, which persists the blob to the slice-036 artifact store and
// recomputes the walkthrough canonical_hash. Same auth gate as creation
// applies upstream (IsAdmin OR grc_engineer) — 403 is forwarded verbatim.
// proxyBoundedUpload (#2344, #2583) caps the body while it streams; at a
// 100 MiB cap the BFF does not buffer it. The platform parses the
// multipart and answers 400 for a malformed one.

import { NextRequest } from "next/server";

import { rejectNonUUID } from "@/lib/api/bff";
import { proxyBoundedUpload } from "@/lib/api/upload";

// MAX_UPLOAD_BYTES mirrors the handler's MaxBytesReader in
// internal/api/walkthroughs: MaxAttachmentBytes (artifact.MaxUploadBytes,
// 100 MiB) plus 1 KiB for the multipart envelope.
const MAX_UPLOAD_BYTES = (100 << 20) + 1024;

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  return proxyBoundedUpload(
    req,
    `/v1/walkthroughs/${encodeURIComponent(id)}/attachments`,
    {
      maxBytes: MAX_UPLOAD_BYTES,
      tooLarge: "attachment exceeds the 100 MiB upload cap",
      notMultipart: "invalid multipart body",
    },
  );
}
