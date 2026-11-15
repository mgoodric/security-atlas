// #1785 — BFF for POST /v1/controls/imports: the control sheet upload.
//
// The multipart body (file, framework, optional repeated column_map) is
// re-posed verbatim; the platform's size, row and column caps and its
// header detection are the single source of truth. Status and body pass
// through (201 with the staged batch, 400/413 with the reason).
//
// Before buffering anything the route checks the bearer, refuses a body
// that declares more than the platform accepts, and requires
// multipart/form-data (the #1794 import route's order).

import { cookies } from "next/headers";
import { NextResponse, type NextRequest } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// MAX_UPLOAD_BYTES mirrors controlimport.MaxUploadBytes (5 MiB) plus the
// handler's multipartOverhead (64 KiB) in internal/api/controlimports.
const MAX_UPLOAD_BYTES = (5 << 20) + (64 << 10);

export async function POST(req: NextRequest) {
  const jar = await cookies();
  if (!jar.get(ATLAS_JWT_COOKIE)?.value) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const declared = Number(req.headers.get("content-length") ?? "0");
  if (declared > MAX_UPLOAD_BYTES) {
    return NextResponse.json(
      {
        error: "upload exceeds the 5 MiB size cap; split it into smaller files",
      },
      { status: 413 },
    );
  }
  const contentType = req.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("multipart/form-data")) {
    return NextResponse.json(
      { error: "request must be multipart/form-data with a file field" },
      { status: 400 },
    );
  }
  let form: FormData;
  try {
    form = await req.formData();
  } catch {
    return NextResponse.json(
      { error: "request must be multipart/form-data with a file field" },
      { status: 400 },
    );
  }
  return forwardMultipart("/v1/controls/imports", form);
}
