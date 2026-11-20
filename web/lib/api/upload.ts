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
// The small-cap routes buffer the bounded bytes (`readBoundedUpload`) and
// parse the multipart from them (`parseBoundedForm`). The large-cap routes
// (walkthrough attachments, artifacts) stream the body to the platform
// through the same count (`proxyBoundedUpload`, #2583) instead of holding
// it. None calls `req.formData()` on the raw request.
// The Go handlers keep their own caps; this protects the BFF process.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
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

type UploadHeaders = { bearer: string; contentType: string };
type Checked = { ok: true; headers: UploadHeaders };

const DECIMAL = /^\d+$/;

// checkHeaders checks the bearer, the declared length and the content
// type, in that order, so nothing is read from an unauthenticated or
// oversize request.
async function checkHeaders(
  req: Request,
  limits: UploadLimits,
): Promise<Checked | Refused> {
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
    return tooLarge(limits);
  }
  const contentType = req.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("multipart/form-data")) {
    return notMultipart(limits);
  }
  return { ok: true, headers: { bearer, contentType } };
}

// readBoundedUpload checks the headers, then buffers the body, counting
// it while it streams. For the small-cap routes that parse the form.
export async function readBoundedUpload(
  req: Request,
  limits: UploadLimits,
): Promise<Result> {
  const checked = await checkHeaders(req, limits);
  if (!checked.ok) return checked;
  const { bearer, contentType } = checked.headers;

  const chunks: Uint8Array[] = [];
  let total = 0;
  const reader = req.body?.getReader();
  if (reader) {
    for (;;) {
      let step: ReadableStreamReadResult<Uint8Array>;
      try {
        step = await reader.read();
      } catch {
        return interrupted();
      }
      if (step.done) break;
      total += step.value.byteLength;
      if (total > limits.maxBytes) {
        await reader.cancel().catch(() => undefined);
        return tooLarge(limits);
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

// proxyBoundedUpload checks the headers, then streams the body to the
// platform without buffering it (#2583). The body passes through a
// counting stream that pulls a chunk from the client only when the
// upstream fetch asks for one, so the BFF holds about one chunk per upload
// whatever the cap. Past the cap the stream errors and the answer is 413;
// a client stream that breaks mid-read is 400 "upload interrupted".
// Otherwise the platform's status and body pass through.
export async function proxyBoundedUpload(
  req: Request,
  upstreamPath: string,
  limits: UploadLimits,
): Promise<NextResponse> {
  const checked = await checkHeaders(req, limits);
  if (!checked.ok) return checked.response;
  const { bearer, contentType } = checked.headers;
  const counted = countedBody(req.body, limits.maxBytes);
  let upstream: Response;
  let text: string;
  try {
    upstream = await fetch(`${apiBaseURL()}${upstreamPath}`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": contentType,
      },
      body: counted.body,
      duplex: "half",
      cache: "no-store",
    } as RequestInit & { duplex: "half" });
    text = await upstream.text();
  } catch {
    return (
      counted.refusal(limits) ?? fail(502, "upstream unavailable").response
    );
  }
  // The platform can answer before it has read the whole body; a body
  // that ran past the cap is refused all the same.
  const refusal = counted.refusal(limits);
  if (refusal) return refusal;
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

type CountedBody = {
  body: ReadableStream<Uint8Array>;
  // The 413 or 400 for a body cut off by the cap or by a broken client
  // stream; null when the body streamed normally.
  refusal: (limits: UploadLimits) => NextResponse | null;
};

function countedBody(
  source: ReadableStream<Uint8Array> | null,
  maxBytes: number,
): CountedBody {
  let state: "ok" | "tooLarge" | "interrupted" = "ok";
  let total = 0;
  const reader = source?.getReader();
  // highWaterMark 0: nothing queues here. Each pull is one client chunk,
  // made when the upstream fetch reads.
  const body = new ReadableStream<Uint8Array>(
    {
      async pull(controller) {
        if (!reader) {
          controller.close();
          return;
        }
        let step: ReadableStreamReadResult<Uint8Array>;
        try {
          step = await reader.read();
        } catch (err) {
          state = "interrupted";
          controller.error(err);
          return;
        }
        if (step.done) {
          controller.close();
          return;
        }
        total += step.value.byteLength;
        if (total > maxBytes) {
          state = "tooLarge";
          await reader.cancel().catch(() => undefined);
          controller.error(new Error("upload exceeds its cap"));
          return;
        }
        controller.enqueue(step.value);
      },
      cancel(reason) {
        return reader?.cancel(reason);
      },
    },
    { highWaterMark: 0 },
  );
  return {
    body,
    refusal: (limits) => {
      if (state === "tooLarge") return tooLarge(limits).response;
      if (state === "interrupted") return interrupted().response;
      return null;
    },
  };
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

function fail(status: number, error: string): Refused {
  return { ok: false, response: NextResponse.json({ error }, { status }) };
}

function tooLarge(limits: UploadLimits): Refused {
  return fail(
    413,
    limits.tooLarge ?? `upload exceeds ${limits.maxBytes} bytes`,
  );
}

function notMultipart(limits: UploadLimits): Refused {
  return fail(
    400,
    limits.notMultipart ?? "content-type must be multipart/form-data",
  );
}

function interrupted(): Refused {
  return fail(400, "upload interrupted");
}
