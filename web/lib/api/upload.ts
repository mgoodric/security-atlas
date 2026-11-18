// #2344 — shared size gate for the multipart upload BFFs.
//
// A cap on the declared Content-Length alone is not a cap: a chunked body
// has no length, `Number("")`/`Number("abc")` gives 0/NaN, the comparison
// passes, and `req.formData()` buffers whatever arrives. This helper is the
// one place an upload route reads its body, in two layers:
//
//   1. Content-Length must be present and a non-negative integer (411), and
//      no larger than the route's cap (413). Browser fetch with a FormData
//      body always sends it.
//   2. The body is counted while it streams; the read is cancelled with 413
//      as soon as the count passes the cap, so a header that understates
//      the body never buys more than cap+one chunk of memory.
//
// Routes parse the multipart from the bounded bytes (`parseBoundedForm`)
// or forward them as-is; none calls `req.formData()` on the raw request.
// The Go handlers keep their own caps; this protects the BFF process.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export type UploadLimits = {
  // Largest body, in bytes, the route accepts.
  maxBytes: number;
  // 413 message; defaults to a generic one naming the cap.
  tooLarge?: string;
  // 400 message for a body that is not multipart/form-data.
  notMultipart?: string;
};

export type BoundedUpload = {
  bearer: string;
  contentType: string;
  bytes: Uint8Array<ArrayBuffer>;
};

type Accepted = { ok: true; upload: BoundedUpload };
type Refused = { ok: false; response: NextResponse };
type Result = Accepted | Refused;

const DECIMAL = /^\d+$/;

// readBoundedUpload checks the bearer, the declared length, the content
// type and then the streamed length, in that order, so nothing is read
// from an unauthenticated or oversize request.
export async function readBoundedUpload(
  req: Request,
  limits: UploadLimits,
): Promise<Result> {
  const tooLarge = () =>
    fail(413, limits.tooLarge ?? `upload exceeds ${limits.maxBytes} bytes`);
  const notMultipart = () =>
    fail(
      400,
      limits.notMultipart ?? "content-type must be multipart/form-data",
    );

  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return fail(401, "unauthenticated");
  }
  const rawLength = req.headers.get("content-length")?.trim() ?? "";
  if (!DECIMAL.test(rawLength) || !Number.isSafeInteger(Number(rawLength))) {
    return fail(411, "upload must declare its Content-Length");
  }
  if (Number(rawLength) > limits.maxBytes) {
    return tooLarge();
  }
  const contentType = req.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("multipart/form-data")) {
    return notMultipart();
  }

  const chunks: Uint8Array[] = [];
  let total = 0;
  const reader = req.body?.getReader();
  if (reader) {
    for (;;) {
      let step: ReadableStreamReadResult<Uint8Array>;
      try {
        step = await reader.read();
      } catch {
        return notMultipart();
      }
      if (step.done) break;
      total += step.value.byteLength;
      if (total > limits.maxBytes) {
        await reader.cancel().catch(() => undefined);
        return tooLarge();
      }
      chunks.push(step.value);
    }
  }
  const bytes = new Uint8Array(total);
  let at = 0;
  for (const c of chunks) {
    bytes.set(c, at);
    at += c.byteLength;
  }
  return { ok: true, upload: { bearer, contentType, bytes } };
}

// parseBoundedForm parses the multipart from the already-bounded bytes.
// Returns null when the body is not valid multipart.
export async function parseBoundedForm(
  upload: BoundedUpload,
): Promise<FormData | null> {
  try {
    return await new Response(upload.bytes, {
      headers: { "content-type": upload.contentType },
    }).formData();
  } catch {
    return null;
  }
}

function fail(status: number, error: string): Result {
  return { ok: false, response: NextResponse.json({ error }, { status }) };
}
