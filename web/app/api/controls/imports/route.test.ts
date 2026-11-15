// #1785 — vitest coverage for the control import BFF routes: each forwards
// the bearer to its /v1/controls/imports path and passes the platform's
// status and body through. Path ids must be UUIDs, the upload is checked
// before it is buffered, and decision bodies are allow-listed.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST as approveClear } from "./[id]/approve-clear/route";
import { GET as exportBatch } from "./[id]/export/route";
import { GET as getBatch } from "./[id]/route";
import { POST as approve } from "./[id]/rows/[row]/approve/route";
import { POST as reject } from "./[id]/rows/[row]/reject/route";
import { GET as listRows } from "./[id]/rows/route";
import { POST as upload } from "./route";

const B = "b1785000-0000-4000-8000-000000000001";
const R = "b1785000-0000-4000-8000-0000000000a1";
const UP = `http://atlas:8080/v1/controls/imports/${B}`;

const batch = () => ({ params: Promise.resolve({ id: B }) });
const batchRow = () => ({ params: Promise.resolve({ id: B, row: R }) });

function upstream(status: number, body: unknown, headers = {}) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json", ...headers },
    }),
  );
}

function call(spy: ReturnType<typeof upstream>) {
  const [url, init] = spy.mock.calls[0] ?? [];
  return { url: String(url), init: init as RequestInit };
}

function jsonReq(body: string) {
  return { json: async () => JSON.parse(body) } as never;
}

function uploadReq(
  form: FormData,
  headers: Record<string, string> = {
    "content-type": "multipart/form-data; boundary=x",
  },
) {
  const h = new Headers(headers);
  return {
    headers: h,
    formData: async () => form,
  } as never;
}

describe("control import BFF routes", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  test("401 without a bearer cookie", async () => {
    mockCookieGet.mockReturnValue(undefined);
    expect((await getBatch(new Request("http://t/"), batch())).status).toBe(
      401,
    );
    expect((await exportBatch(new Request("http://t/"), batch())).status).toBe(
      401,
    );
  });

  test("every [id] and [row] route refuses a non-UUID before calling upstream", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const dotID = { params: Promise.resolve({ id: ".." }) };
    const req = new Request("http://t/");
    for (const res of [
      await getBatch(req, dotID),
      await listRows(req, dotID),
      await approveClear(req, dotID),
      await exportBatch(req, dotID),
      await approve(jsonReq("{}"), {
        params: Promise.resolve({ id: B, row: ".." }),
      }),
      await reject(jsonReq("{}"), {
        params: Promise.resolve({ id: "..", row: R }),
      }),
    ]) {
      expect(res.status).toBe(400);
    }
    expect(spy).not.toHaveBeenCalled();
  });

  test("upload forwards the multipart form", async () => {
    const spy = upstream(201, { id: B });
    const form = new FormData();
    form.append("framework", "soc2");
    const res = await upload(uploadReq(form));
    expect(res.status).toBe(201);
    const { url, init } = call(spy);
    expect(url).toBe("http://atlas:8080/v1/controls/imports");
    expect(init.body).toBeInstanceOf(FormData);
    expect((init.headers as Record<string, string>).Authorization).toBe(
      `Bearer ${TEST_BEARER_DEFAULT}`,
    );
  });

  test("upload checks the bearer before reading the body", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const formData = vi.fn();
    const res = await upload({
      headers: new Headers({ "content-type": "multipart/form-data; b=x" }),
      formData,
    } as never);
    expect(res.status).toBe(401);
    expect(formData).not.toHaveBeenCalled();
  });

  test("upload refuses an oversize declared body with 413", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const formData = vi.fn();
    const res = await upload({
      headers: new Headers({
        "content-type": "multipart/form-data; boundary=x",
        "content-length": String(6 << 20),
      }),
      formData,
    } as never);
    expect(res.status).toBe(413);
    expect(formData).not.toHaveBeenCalled();
    expect(spy).not.toHaveBeenCalled();
  });

  test("upload refuses a non-multipart or unparseable body with 400", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const json = await upload(
      uploadReq(new FormData(), { "content-type": "application/json" }),
    );
    expect(json.status).toBe(400);
    const broken = await upload({
      headers: new Headers({ "content-type": "multipart/form-data; b=x" }),
      formData: async () => {
        throw new TypeError("bad multipart");
      },
    } as never);
    expect(broken.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("rows forwards only the known filters", async () => {
    const spy = upstream(200, { rows: [] });
    await listRows(
      new Request(
        `http://t/api/controls/imports/${B}/rows?status=pending&no_candidate_reaches=true&evil=1`,
      ),
      batch(),
    );
    expect(call(spy).url).toBe(
      `${UP}/rows?status=pending&no_candidate_reaches=true`,
    );
  });

  test("approve forwards the allow-listed body and passes a 409 through", async () => {
    const spy = upstream(409, { error: "already decided" });
    const res = await approve(
      jsonReq(
        '{"candidate_rank":2,"implementation_type":"automated","applicability_expr":{"op":"true"},"approver":"someone-else"}',
      ),
      batchRow(),
    );
    expect(res.status).toBe(409);
    const { url, init } = call(spy);
    expect(url).toBe(`${UP}/rows/${R}/approve`);
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      candidate_rank: 2,
      implementation_type: "automated",
      applicability_expr: { op: "true" },
    });
  });

  test("approve refuses a non-object body before calling upstream", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const thrown = await approve(
      {
        json: async () => {
          throw new SyntaxError("bad");
        },
      } as never,
      batchRow(),
    );
    expect(thrown.status).toBe(400);
    expect((await approve(jsonReq("[1]"), batchRow())).status).toBe(400);
    expect((await approve(jsonReq("null"), batchRow())).status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("reject forwards only the reason", async () => {
    const spy = upstream(200, { status: "rejected" });
    await reject(jsonReq('{"reason":"not ours","extra":1}'), batchRow());
    const { url, init } = call(spy);
    expect(url).toBe(`${UP}/rows/${R}/reject`);
    expect(JSON.parse(String(init.body))).toEqual({ reason: "not ours" });
  });

  test("approve-clear posts without a body", async () => {
    const spy = upstream(409, { error: "approve-clear already running" });
    const res = await approveClear(new Request("http://t/"), batch());
    expect(res.status).toBe(409);
    const { url, init } = call(spy);
    expect(url).toBe(`${UP}/approve-clear`);
    expect(init.method).toBe("POST");
    expect(init.body).toBeUndefined();
  });

  test("export streams the archive with the platform's filename", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("tar-bytes", {
        status: 200,
        headers: {
          "Content-Type": "application/gzip",
          "Content-Disposition": 'attachment; filename="controls.tar.gz"',
        },
      }),
    );
    const res = await exportBatch(new Request("http://t/"), batch());
    expect(res.status).toBe(200);
    expect(res.headers.get("content-disposition")).toBe(
      'attachment; filename="controls.tar.gz"',
    );
  });
});
