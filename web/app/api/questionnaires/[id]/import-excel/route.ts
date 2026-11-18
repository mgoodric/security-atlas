// Slice 263 — BFF proxy for `POST /v1/questionnaires/{id}/import-excel`.
//
// Streams a multipart/form-data Excel upload to the platform. Mirrors
// the audit-workspace forwardMultipart helper (web/lib/api/bff.ts) —
// we forward the FormData verbatim so the upstream's MaxBytesReader +
// excelize parsing logic is the single source of truth for size +
// shape validation.
//
// Wire shape:
//   POST /api/questionnaires/{id}/import-excel
//     body: FormData with `file` field (.xlsx, <= 5MB)
//   -> { questions: Question[], unmapped_columns: string[] }
//
// The platform caps the upload at questionnaire.MaxUploadBytes (5MB).
// The list page additionally validates client-side (UX courtesy) so
// oversize files never leave the browser — see upload-zone.tsx.
// readBoundedUpload (#2344) holds the BFF to the same cap on the declared
// and the streamed length, so the BFF never buffers more than that.
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only. RLS enforces tenancy at DB.
//   * P0-263-4: consumes the slice 155 ImportExcel handler verbatim.

import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { rejectNonUUID } from "@/lib/api/bff";
import { parseBoundedForm, readBoundedUpload } from "@/lib/api/upload";

// MAX_UPLOAD_BYTES mirrors questionnaire.MaxUploadBytes (5 MiB), which the
// handler applies to the whole request body via MaxBytesReader.
const MAX_UPLOAD_BYTES = 5 * 1024 * 1024;

const NOT_MULTIPART = "request must be multipart/form-data with a file field";

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const read = await readBoundedUpload(req, {
    maxBytes: MAX_UPLOAD_BYTES,
    tooLarge: "upload exceeds the 5 MB size cap",
    notMultipart: NOT_MULTIPART,
  });
  if (!read.ok) return read.response;
  const form = await parseBoundedForm(read.upload);
  if (!form) {
    return NextResponse.json({ error: NOT_MULTIPART }, { status: 400 });
  }
  // fetch() with a FormData body sets the correct multipart boundary
  // header automatically.
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(id)}/import-excel`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${read.upload.bearer}` },
      body: form,
      cache: "no-store",
    },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
