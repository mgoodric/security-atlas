// #2344 — request builders for the upload BFF tests (lib/api/upload.ts).
//
// multipartRequest is what a browser sends: the encoded FormData with its
// boundary content-type and a true Content-Length. streamingRequest is a
// body that arrives in chunks under whatever headers the test chooses (none,
// a garbage length, a length that understates the body), and reports how
// many chunks the route actually pulled.

export async function multipartRequest(
  form: FormData,
  url = "http://t/upload",
): Promise<Request> {
  const encoded = new Request(url, { method: "POST", body: form });
  const bytes = new Uint8Array(await encoded.arrayBuffer());
  return new Request(url, {
    method: "POST",
    headers: {
      "content-type": encoded.headers.get("content-type") ?? "",
      "content-length": String(bytes.byteLength),
    },
    body: bytes,
  });
}

// tailBytes appends one more chunk of that size, for a body that is not a
// whole number of chunks. failAfter breaks the stream once that many
// chunks have been pulled, as a client that drops mid-upload does.
export function streamingRequest(opts: {
  chunks: number;
  chunkBytes: number;
  tailBytes?: number;
  failAfter?: number;
  headers?: Record<string, string>;
  url?: string;
}): { req: Request; pulled: () => number } {
  let pulled = 0;
  // highWaterMark 0: nothing is pulled until a reader asks, so pulled()
  // counts only the chunks the route read.
  const body = new ReadableStream<Uint8Array>(
    {
      pull(controller) {
        if (opts.failAfter !== undefined && pulled >= opts.failAfter) {
          controller.error(new Error("client went away"));
          return;
        }
        if (pulled < opts.chunks) {
          pulled += 1;
          controller.enqueue(new Uint8Array(opts.chunkBytes));
          return;
        }
        if (opts.tailBytes && pulled === opts.chunks) {
          pulled += 1;
          controller.enqueue(new Uint8Array(opts.tailBytes));
          return;
        }
        controller.close();
      },
    },
    { highWaterMark: 0 },
  );
  const req = new Request(opts.url ?? "http://t/upload", {
    method: "POST",
    headers: {
      "content-type": "multipart/form-data; boundary=x",
      ...opts.headers,
    },
    body,
    duplex: "half",
  } as RequestInit & { duplex: "half" });
  return { req, pulled: () => pulled };
}
