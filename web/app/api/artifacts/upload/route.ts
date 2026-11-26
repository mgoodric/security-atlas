import { NextRequest } from "next/server";

import { proxyBoundedUpload } from "@/lib/api/upload";

// Slice 011 — server-side proxy for POST /v1/artifacts:upload (slice 036).
// The browser posts a multipart form; we forward the body bytes unchanged
// so we never have to decode the file in JS. Slice 036's handler enforces
// the tenant-prefixed S3 storage; we only thread auth through.
//
// Body cap mirrors slice 011's spec (≤10 MiB attestation artifact). Slice
// 036 still enforces the absolute 100 MiB cap on its side; if our cap
// is hit first the user gets a friendly 413 instead of a slice-036
// surface error. proxyBoundedUpload (#2344, #2583) enforces it on the
// declared and the streamed length, streaming the body through rather
// than buffering it.

const MAX_ATTESTATION_ARTIFACT_BYTES = 10 * 1024 * 1024;

export async function POST(req: NextRequest) {
  return proxyBoundedUpload(req, "/v1/artifacts:upload", {
    maxBytes: MAX_ATTESTATION_ARTIFACT_BYTES,
    tooLarge: `attestation artifact exceeds ${MAX_ATTESTATION_ARTIFACT_BYTES} bytes`,
  });
}
