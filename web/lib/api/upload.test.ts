// #2344 — vitest coverage for readBoundedUpload: the size gate every
// upload BFF reads its body through. Each refusal must happen without the
// raw request's formData() being called.

import { beforeEach, describe, expect, test, vi } from "vitest";

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

import { parseBoundedForm, readBoundedUpload } from "./upload";

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

  test.each(["abc", "", "-1", "1.5", "1e3", "NaN", "Infinity", "0x10"])(
    "411 for a Content-Length of %j",
    async (length) => {
      const { req } = streamingRequest({
        chunks: 1,
        chunkBytes: 16,
        headers: { "content-length": length },
      });
      await refused(req, 411);
    },
  );

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
