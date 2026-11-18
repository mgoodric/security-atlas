import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { readBoundedUpload } from "@/lib/api/upload";

// Slice 011 — server-side proxy for POST /v1/artifacts:upload (slice 036).
// The browser posts a multipart form; we forward the body bytes unchanged
// so we never have to decode the file in JS. Slice 036's handler enforces
// the tenant-prefixed S3 storage; we only thread auth through.
//
// Body cap mirrors slice 011's spec (≤10 MiB attestation artifact). Slice
// 036 still enforces the absolute 100 MiB cap on its side; if our cap
// is hit first the user gets a friendly 413 instead of a slice-036
// surface error. readBoundedUpload (#2344) enforces it on the declared
// and the streamed length.

const MAX_ATTESTATION_ARTIFACT_BYTES = 10 * 1024 * 1024;

export async function POST(req: NextRequest) {
  const read = await readBoundedUpload(req, {
    maxBytes: MAX_ATTESTATION_ARTIFACT_BYTES,
    tooLarge: `attestation artifact exceeds ${MAX_ATTESTATION_ARTIFACT_BYTES} bytes`,
  });
  if (!read.ok) return read.response;
  const { bearer, contentType, bytes } = read.upload;
  const upstream = await fetch(`${apiBaseURL()}/v1/artifacts:upload`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": contentType,
    },
    body: bytes,
  });
  const raw = await upstream.text();
  return new NextResponse(raw, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
