// #2344 — vitest coverage for readBoundedUpload: the size gate every
// upload BFF reads its body through. Each refusal must happen without the
// raw request's formData() being called.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../test-utils/test-tokens";
import {
  multipartRequest,
  streamingRequest,
} from "../test-utils/upload-requests";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import {
  parseBoundedForm,
  proxyBoundedUpload,
  readBoundedUpload,
} from "./upload";
import { UPSTREAM_5XX_BODY } from "./bff";

const CAP = 64 * 1024;

async function refused(req: Request, status: number) {
  const formData = vi.spyOn(req, "formData");
  const read = await readBoundedUpload(req, { maxBytes: CAP });
  expect(read.ok).toBe(false);
  if (!read.ok) expect(read.response.status).toBe(status);
  expect(formData).not.toHaveBeenCalled();
}

describe("readBoundedUpload", () => {
  beforeEach(() => {
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
  });

  test("401 without a bearer cookie, before the body is read", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const { req, pulled } = streamingRequest({ chunks: 4, chunkBytes: 1024 });
    await refused(req, 401);
    expect(pulled()).toBe(0);
  });

  test("411 for a chunked body with no Content-Length", async () => {
    const { req, pulled } = streamingRequest({ chunks: 4, chunkBytes: 1024 });
    expect(req.headers.get("content-length")).toBeNull();
    await refused(req, 411);
    expect(pulled()).toBe(0);
  });

  test.each([
    "abc",
    "",
    "-1",
    "1.5",
    "1e3",
    "NaN",
    "Infinity",
    "0x10",
    "5, 5",
    "+5",
    " 1e9",
  ])("411 for a Content-Length of %j", async (length) => {
    const { req } = streamingRequest({
      chunks: 1,
      chunkBytes: 16,
      headers: { "content-length": length },
    });
    await refused(req, 411);
  });

  test("413 for a declared length over the cap, before the body is read", async () => {
    const { req, pulled } = streamingRequest({
      chunks: 4,
      chunkBytes: 1024,
      headers: { "content-length": String(CAP + 1) },
    });
    await refused(req, 413);
    expect(pulled()).toBe(0);
  });

  test("413 mid-stream when the header understates the body", async () => {
    const chunks = 64;
    const { req, pulled } = streamingRequest({
      chunks,
      chunkBytes: 8 * 1024, // 512 KiB in all, 8x the cap
      headers: { "content-length": "100" },
    });
    await refused(req, 413);
    // The read stopped just past the cap, not at the end of the body.
    expect(pulled()).toBeGreaterThan(CAP / (8 * 1024));
    expect(pulled()).toBeLessThan(chunks / 2);
  });

  test("400 for a body that is not multipart/form-data", async () => {
    const { req } = streamingRequest({
      chunks: 1,
      chunkBytes: 16,
      headers: { "content-type": "application/json", "content-length": "16" },
    });
    await refused(req, 400);
  });

  test("400 upload interrupted when the client stream breaks mid-read", async () => {
    const { req } = streamingRequest({
      chunks: 8,
      chunkBytes: 1024,
      failAfter: 2,
      headers: { "content-length": String(8 * 1024) },
    });
    const read = await readBoundedUpload(req, { maxBytes: CAP });
    expect(read.ok).toBe(false);
    if (read.ok) return;
    expect(read.response.status).toBe(400);
    expect(await read.response.json()).toEqual({ error: "upload interrupted" });
  });

  test("a declared length of exactly the cap is accepted, cap + 1 refused", async () => {
    const atCap = streamingRequest({
      chunks: CAP / 1024,
      chunkBytes: 1024,
      headers: { "content-length": String(CAP) },
    });
    const read = await readBoundedUpload(atCap.req, { maxBytes: CAP });
    expect(read.ok).toBe(true);
    if (read.ok) expect(read.upload.bytes.byteLength).toBe(CAP);

    const overCap = streamingRequest({
      chunks: CAP / 1024,
      chunkBytes: 1024,
      tailBytes: 1,
      headers: { "content-length": String(CAP + 1) },
    });
    await refused(overCap.req, 413);
    expect(overCap.pulled()).toBe(0);
  });

  test("a streamed body of exactly the cap is accepted, cap + 1 refused", async () => {
    // The header understates both bodies, so only the streamed count
    // decides.
    const atCap = streamingRequest({
      chunks: CAP / 1024,
      chunkBytes: 1024,
      headers: { "content-length": "100" },
    });
    const read = await readBoundedUpload(atCap.req, { maxBytes: CAP });
    expect(read.ok).toBe(true);
    if (read.ok) expect(read.upload.bytes.byteLength).toBe(CAP);

    const overCap = streamingRequest({
      chunks: CAP / 1024,
      chunkBytes: 1024,
      tailBytes: 1,
      headers: { "content-length": "100" },
    });
    await refused(overCap.req, 413);
  });

  test("a valid upload comes back byte-for-byte with its content type", async () => {
    const form = new FormData();
    form.append("framework", "soc2");
    form.append("file", new Blob(["a,b\n1,2\n"]), "sheet.csv");
    const req = await multipartRequest(form);
    const original = new Uint8Array(await req.clone().arrayBuffer());
    const formData = vi.spyOn(req, "formData");

    const read = await readBoundedUpload(req, { maxBytes: CAP });
    expect(read.ok).toBe(true);
    if (!read.ok) return;
    expect(read.upload.bearer).toBe(TEST_BEARER_DEFAULT);
    expect(read.upload.contentType).toBe(req.headers.get("content-type"));
    expect(read.upload.bytes).toEqual(original);
    expect(formData).not.toHaveBeenCalled();

    const parsed = await parseBoundedForm(read.upload);
    expect(parsed?.get("framework")).toBe("soc2");
    const file = parsed?.get("file") as File;
    expect(file.name).toBe("sheet.csv");
    expect(await file.text()).toBe("a,b\n1,2\n");
  });

  test("parseBoundedForm returns null for a body that is not valid multipart", async () => {
    const parsed = await parseBoundedForm({
      bearer: "b",
      contentType: "multipart/form-data; boundary=x",
      bytes: new TextEncoder().encode("not multipart"),
    });
    expect(parsed).toBeNull();
  });
});

// #2583 — proxyBoundedUpload streams the body to the platform instead of
// buffering it. The fetch stand-in reads the forwarded body the way the
// platform would, one chunk at a time.
describe("proxyBoundedUpload", () => {
  const UPSTREAM = "/v1/walkthroughs/x/attachments";

  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  // drainingUpstream reads the forwarded body to the end and answers 201
  // with its length, or rejects when the body errors, as fetch does.
  // onChunk sees the running count after each chunk and may stall the
  // reader, as a slow platform would.
  function drainingUpstream(
    onChunk?: (received: number) => void | Promise<void>,
  ) {
    return vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (_url, init) => {
        const reader = (init?.body as ReadableStream<Uint8Array>).getReader();
        let received = 0;
        let bytes = 0;
        for (;;) {
          const step = await reader.read();
          if (step.done) break;
          received += 1;
          bytes += step.value.byteLength;
          await onChunk?.(received);
        }
        return new Response(JSON.stringify({ bytes }), { status: 201 });
      });
  }

  test("refuses on the headers before calling the platform", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const { req, pulled } = streamingRequest({ chunks: 4, chunkBytes: 1024 });
    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(411);
    expect(pulled()).toBe(0);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("streams a valid upload through with its bearer and content type", async () => {
    const form = new FormData();
    form.append("annotations", "{}");
    form.append("file", new Blob(["walkthrough-bytes"]), "w.png");
    const req = await multipartRequest(form);
    const original = new Uint8Array(await req.clone().arrayBuffer());
    let sent: Uint8Array | undefined;
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (_url, init) => {
        sent = new Uint8Array(
          await new Response(init?.body as ReadableStream).arrayBuffer(),
        );
        return new Response(JSON.stringify({ id: "a" }), { status: 201 });
      });

    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(201);
    expect(await res.json()).toEqual({ id: "a" });
    const [url, init] = fetchSpy.mock.calls[0] ?? [];
    expect(String(url)).toBe(`http://atlas:8080${UPSTREAM}`);
    const headers = init?.headers as Record<string, string>;
    expect(headers.Authorization).toBe(`Bearer ${TEST_BEARER_DEFAULT}`);
    expect(headers["Content-Type"]).toBe(req.headers.get("content-type"));
    expect(sent).toEqual(original);
  });

  test("holds at most one client chunk ahead of the platform", async () => {
    // Peak retention: each chunk is pulled from the client only when the
    // platform reads one, so the BFF never queues the body. The platform
    // here is slow (a macrotask per chunk), which is when a buffering
    // proxy would race ahead of it.
    const chunks = 48;
    const { req, pulled } = streamingRequest({
      chunks,
      chunkBytes: 1024,
      headers: { "content-length": String(chunks * 1024) },
    });
    let ahead = 0;
    drainingUpstream(async (received) => {
      await new Promise((resolve) => setTimeout(resolve, 0));
      ahead = Math.max(ahead, pulled() - received);
    });
    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(201);
    expect(await res.json()).toEqual({ bytes: chunks * 1024 });
    expect(ahead).toBeLessThanOrEqual(1);
  });

  test("413 mid-stream when the header understates the body", async () => {
    const chunks = 64;
    const { req, pulled } = streamingRequest({
      chunks,
      chunkBytes: 8 * 1024, // 512 KiB in all, 8x the cap
      headers: { "content-length": "100" },
    });
    drainingUpstream();
    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(413);
    expect(pulled()).toBeLessThan(chunks / 2);
  });

  test("a streamed body of exactly the cap passes, cap + 1 is 413", async () => {
    const atCap = streamingRequest({
      chunks: CAP / 1024,
      chunkBytes: 1024,
      headers: { "content-length": "100" },
    });
    drainingUpstream();
    const ok = await proxyBoundedUpload(atCap.req, UPSTREAM, {
      maxBytes: CAP,
    });
    expect(ok.status).toBe(201);
    expect(await ok.json()).toEqual({ bytes: CAP });

    const overCap = streamingRequest({
      chunks: CAP / 1024,
      chunkBytes: 1024,
      tailBytes: 1,
      headers: { "content-length": "100" },
    });
    const refused = await proxyBoundedUpload(overCap.req, UPSTREAM, {
      maxBytes: CAP,
      tooLarge: "too big",
    });
    expect(refused.status).toBe(413);
    expect(await refused.json()).toEqual({ error: "too big" });
  });

  test("413 even when the platform answers before reading past the cap", async () => {
    const { req } = streamingRequest({
      chunks: 16,
      chunkBytes: 8 * 1024,
      headers: { "content-length": "100" },
    });
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, init) => {
      // Read past the cap, then answer without surfacing the error.
      const reader = (init?.body as ReadableStream<Uint8Array>).getReader();
      await reader.read().catch(() => undefined);
      for (let i = 0; i < 16; i++) {
        const step = await reader.read().catch(() => ({ done: true }));
        if (step.done) break;
      }
      return new Response("{}", { status: 201 });
    });
    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(413);
  });

  test("400 upload interrupted when the client stream breaks", async () => {
    const { req } = streamingRequest({
      chunks: 8,
      chunkBytes: 1024,
      failAfter: 3,
      headers: { "content-length": String(8 * 1024) },
    });
    drainingUpstream();
    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(400);
    expect(await res.json()).toEqual({ error: "upload interrupted" });
  });

  // #2704 — the platform's 5xx body stays off the wire; a 4xx passes.
  test("replaces a platform 5xx body, relays a 4xx body", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    fetchSpy.mockResolvedValueOnce(
      new Response('{"error":"blob put: s3://atlas-private/key denied"}', {
        status: 500,
      }),
    );
    const form = new FormData();
    form.append("file", new Blob(["x"]), "x.bin");
    const broke = await proxyBoundedUpload(
      await multipartRequest(form),
      UPSTREAM,
      { maxBytes: CAP },
    );
    expect(broke.status).toBe(500);
    expect(await broke.text()).toBe(UPSTREAM_5XX_BODY);

    fetchSpy.mockResolvedValueOnce(
      new Response('{"error":"walkthrough is frozen"}', { status: 409 }),
    );
    const refusedRes = await proxyBoundedUpload(
      await multipartRequest(form),
      UPSTREAM,
      { maxBytes: CAP },
    );
    expect(refusedRes.status).toBe(409);
    expect(await refusedRes.json()).toEqual({
      error: "walkthrough is frozen",
    });
  });

  // #2704 — a client that disconnects aborts the platform call, rather
  // than leaving the BFF waiting on it.
  test("passes the client's abort signal to the platform fetch", async () => {
    const form = new FormData();
    form.append("file", new Blob(["x"]), "x.bin");
    const req = await multipartRequest(form);
    let seen: AbortSignal | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, init) => {
      seen = init?.signal ?? undefined;
      return new Response("{}", { status: 201 });
    });
    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(201);
    expect(seen).toBe(req.signal);
  });

  test("502 when the platform cannot be reached", async () => {
    const form = new FormData();
    form.append("file", new Blob(["x"]), "x.bin");
    const req = await multipartRequest(form);
    vi.spyOn(globalThis, "fetch").mockRejectedValue(
      new TypeError("fetch failed"),
    );
    const res = await proxyBoundedUpload(req, UPSTREAM, { maxBytes: CAP });
    expect(res.status).toBe(502);
  });
});
