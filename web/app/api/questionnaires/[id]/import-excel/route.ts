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
//
// Constitutional invariants:
//   * Invariant 6: bearer-forward only. RLS enforces tenancy at DB.
//   * P0-263-4: consumes the slice 155 ImportExcel handler verbatim.

import { cookies } from "next/headers";
import { type NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

export async function POST(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await params;
  // Pull the multipart body from the incoming request and re-pose it
  // to the upstream. fetch() with a FormData body sets the correct
  // multipart boundary header automatically.
  const form = await req.formData();
  const upstream = await fetch(
    `${apiBaseURL()}/v1/questionnaires/${encodeURIComponent(id)}/import-excel`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${bearer}` },
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
