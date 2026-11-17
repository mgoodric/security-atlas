// #2110 — BFF for POST /v1/controls:upload-bundle: Upload new version on
// control detail.
//
// The multipart body (one bundle.tar.gz file part) is re-posed verbatim to
// the existing bundle upload; its admin gate, parser, test gate and
// supersession are the single source of truth. Status and body pass
// through (200/201 with the upload result, 400 with the reason, 403 for a
// non-admin).
//
// Before buffering anything the route checks the bearer, refuses a body
// that declares more than the platform accepts, and requires
// multipart/form-data (the controls import route's order).

import { cookies } from "next/headers";
import { NextResponse, type NextRequest } from "next/server";

import { forwardMultipart } from "@/lib/api/bff";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// MAX_UPLOAD_BYTES mirrors the upload handler's maxRequestBytes (8 MiB) in
// internal/api/controls.
const MAX_UPLOAD_BYTES = 8 << 20;

export async function POST(req: NextRequest) {
  const jar = await cookies();
  if (!jar.get(ATLAS_JWT_COOKIE)?.value) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const declared = Number(req.headers.get("content-length") ?? "0");
  if (declared > MAX_UPLOAD_BYTES) {
    return NextResponse.json(
      { error: "bundle exceeds the 8 MiB upload cap" },
      { status: 413 },
    );
  }
  const contentType = req.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("multipart/form-data")) {
    return NextResponse.json(
      {
        error: "request must be multipart/form-data with a bundle.tar.gz file",
      },
      { status: 400 },
    );
  }
  let form: FormData;
  try {
    form = await req.formData();
  } catch {
    return NextResponse.json(
      {
        error: "request must be multipart/form-data with a bundle.tar.gz file",
      },
      { status: 400 },
    );
  }
  return forwardMultipart("/v1/controls:upload-bundle", form);
}
