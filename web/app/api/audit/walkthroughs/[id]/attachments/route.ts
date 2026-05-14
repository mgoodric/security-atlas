// Slice 042 — audit workspace BFF: walkthrough attachment upload proxy.
//
//   POST /v1/walkthroughs/{id}/attachments   multipart/form-data
//
// Streams the FormData (file + optional annotations JSON) through to the
// platform, which persists the blob to the slice-036 artifact store and
// recomputes the walkthrough canonical_hash. Same auth gate as creation
// applies upstream (IsAdmin OR grc_engineer) — 403 is forwarded verbatim.

import { NextRequest, NextResponse } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  let form: FormData;
  try {
    form = await req.formData();
  } catch {
    return NextResponse.json(
      { error: "invalid multipart body" },
      { status: 400 },
    );
  }
  return forwardMultipart(
    `/v1/walkthroughs/${encodeURIComponent(id)}/attachments`,
    form,
  );
}
