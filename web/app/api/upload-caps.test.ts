// #2344 — every multipart upload BFF reads its body through
// readBoundedUpload. For each route: a chunked body with no length is 411,
// a header that understates the body is cut off mid-stream with 413, and
// neither reaches the raw request's formData(). The buffering routes never
// call the platform for either; the streaming ones (#2583) have opened the
// upstream request when the cap cuts the body off. A valid upload still
// reaches the platform with its fields intact.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../lib/test-utils/test-tokens";
import {
  multipartRequest,
  streamingRequest,
} from "../../lib/test-utils/upload-requests";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST as artifactUpload } from "./artifacts/upload/route";
import { POST as walkthroughAttach } from "./audit/walkthroughs/[id]/attachments/route";
import { POST as controlImport } from "./controls/imports/route";
import { POST as bundleUpload } from "./controls/upload-bundle/route";
import { POST as questionnaireImport } from "./questionnaires/[id]/import-excel/route";

const ID = "b2344000-0000-4000-8000-000000000001";
const ctx = () => ({ params: Promise.resolve({ id: ID }) });

type Route = (req: Request) => Promise<Response>;

const routes: {
  name: string;
  cap: number;
  post: Route;
  upstream: string;
  // streams: the route forwards the body as it arrives (#2583) instead of
  // buffering it, so the platform sees the request begin.
  streams?: boolean;
}[] = [
  {
    name: "controls/imports",
    cap: (5 << 20) + (64 << 10),
    post: (r) => controlImport(r as never),
    upstream: "/v1/controls/imports",
  },
  {
    name: "controls/upload-bundle",
    cap: 8 << 20,
    post: (r) => bundleUpload(r as never),
    upstream: "/v1/controls:upload-bundle",
  },
  {
    name: "artifacts/upload",
    cap: 10 << 20,
    post: (r) => artifactUpload(r as never),
    upstream: "/v1/artifacts:upload",
    streams: true,
  },
  {
    name: "questionnaires/[id]/import-excel",
    cap: 5 << 20,
    post: (r) => questionnaireImport(r as never, ctx()),
    upstream: `/v1/questionnaires/${ID}/import-excel`,
  },
  {
    name: "audit/walkthroughs/[id]/attachments",
    cap: (100 << 20) + 1024,
    post: (r) => walkthroughAttach(r as never, ctx()),
    upstream: `/v1/walkthroughs/${ID}/attachments`,
    streams: true,
  },
];

describe.each(routes)(
  "upload BFF $name",
  ({ cap, post, upstream, streams }) => {
    beforeEach(() => {
      vi.restoreAllMocks();
      mockCookieGet.mockReset();
      mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
      process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    });
    afterEach(() => vi.restoreAllMocks());

    test("411 for a chunked body with no Content-Length", async () => {
      const fetchSpy = vi.spyOn(globalThis, "fetch");
      const { req, pulled } = streamingRequest({ chunks: 4, chunkBytes: 1024 });
      const formData = vi.spyOn(req, "formData");
      const res = await post(req);
      expect(res.status).toBe(411);
      expect(pulled()).toBe(0);
      expect(formData).not.toHaveBeenCalled();
      expect(fetchSpy).not.toHaveBeenCalled();
    });

    test("413 for a declared length over the cap", async () => {
      const fetchSpy = vi.spyOn(globalThis, "fetch");
      const { req, pulled } = streamingRequest({
        chunks: 1,
        chunkBytes: 16,
        headers: { "content-length": String(cap + 1) },
      });
      const res = await post(req);
      expect(res.status).toBe(413);
      expect(pulled()).toBe(0);
      expect(fetchSpy).not.toHaveBeenCalled();
    });

    test("413 mid-stream when the header understates the body", async () => {
      // A streaming route has already opened the upstream request; the
      // stand-in reads the body as the platform would, and the body errors.
      const fetchSpy = vi.spyOn(globalThis, "fetch");
      if (streams) {
        fetchSpy.mockImplementation(async (_url, init) => {
          await new Response(init?.body as ReadableStream).arrayBuffer();
          return new Response("{}", { status: 201 });
        });
      }
      const chunkBytes = 1 << 20;
      const chunks = Math.ceil(cap / chunkBytes) * 4;
      const { req, pulled } = streamingRequest({
        chunks,
        chunkBytes,
        headers: { "content-length": "100" },
      });
      const formData = vi.spyOn(req, "formData");
      const res = await post(req);
      expect(res.status).toBe(413);
      expect(pulled()).toBeLessThan(chunks / 2);
      expect(formData).not.toHaveBeenCalled();
      if (!streams) expect(fetchSpy).not.toHaveBeenCalled();
    });

    test("a valid upload reaches the platform intact", async () => {
      const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        new Response(JSON.stringify({ ok: true }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
      const form = new FormData();
      form.append("framework", "soc2");
      form.append("file", new Blob(["payload-bytes"]), "upload.bin");
      const req = await multipartRequest(form);
      const formData = vi.spyOn(req, "formData");

      const res = await post(req);
      expect(res.status).toBe(201);
      expect(formData).not.toHaveBeenCalled();
      const [url, init] = fetchSpy.mock.calls[0] ?? [];
      expect(String(url)).toBe(`http://atlas:8080${upstream}`);
      const headers = (init as RequestInit).headers as Record<string, string>;
      expect(headers.Authorization).toBe(`Bearer ${TEST_BEARER_DEFAULT}`);
      // Re-read what was sent upstream as multipart and check the fields.
      const sent = await new Request("http://t/", {
        method: "POST",
        headers: headers["Content-Type"]
          ? { "content-type": headers["Content-Type"] }
          : undefined,
        body: (init as RequestInit).body,
        duplex: "half",
      } as RequestInit & { duplex: "half" }).formData();
      expect(sent.get("framework")).toBe("soc2");
      expect(await (sent.get("file") as File).text()).toBe("payload-bytes");
    });
  },
);
