// #2847 — vitest coverage for the risk import review BFF routes: each
// forwards the bearer to its /v1/risks/imports path and passes the
// platform's status and body through. Path ids must be UUIDs, and the
// reject body is allow-listed.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST as approveAll } from "./approve/route";
import { POST as closeBatch } from "./close/route";
import { GET as getBatch } from "./route";
import { POST as approveRow } from "./rows/[row]/approve/route";
import { POST as rejectRow } from "./rows/[row]/reject/route";

const B = "b2847000-0000-4000-8000-000000000001";
const R = "b2847000-0000-4000-8000-0000000000a1";
const UP = `http://atlas:8080/v1/risks/imports/${B}`;

const batch = () => ({ params: Promise.resolve({ id: B }) });
const batchRow = () => ({ params: Promise.resolve({ id: B, row: R }) });

function upstream(status: number, body: unknown) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
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

describe("risk import review BFF routes", () => {
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
  });

  test("every route refuses a non-UUID before calling upstream", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const req = new Request("http://t/");
    const dotID = { params: Promise.resolve({ id: ".." }) };
    for (const res of [
      await getBatch(req, dotID),
      await approveAll(req, dotID),
      await approveRow(req, { params: Promise.resolve({ id: B, row: ".." }) }),
      await rejectRow(jsonReq("{}"), {
        params: Promise.resolve({ id: "..", row: R }),
      }),
    ]) {
      expect(res.status).toBe(400);
    }
    expect(spy).not.toHaveBeenCalled();
  });

  test("GET forwards and is not cached", async () => {
    const spy = upstream(200, { id: B, staged: [] });
    const res = await getBatch(new Request("http://t/"), batch());
    expect(res.status).toBe(200);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    const { url, init } = call(spy);
    expect(url).toBe(UP);
    expect((init.headers as Record<string, string>).Authorization).toBe(
      `Bearer ${TEST_BEARER_DEFAULT}`,
    );
  });

  test("approve all posts with no body", async () => {
    const spy = upstream(200, { batch_id: B, approved_count: 2 });
    const res = await approveAll(new Request("http://t/"), batch());
    expect(res.status).toBe(200);
    const { url, init } = call(spy);
    expect(url).toBe(`${UP}/approve`);
    expect(init.method).toBe("POST");
    expect(init.body).toBeUndefined();
  });

  test("approve one row posts to the row and passes a 409 through", async () => {
    const spy = upstream(409, { error: "staged row is already decided" });
    const res = await approveRow(new Request("http://t/"), batchRow());
    expect(res.status).toBe(409);
    expect(call(spy).url).toBe(`${UP}/rows/${R}/approve`);
  });

  test("approve passes the platform's 403 for a non-approver through", async () => {
    upstream(403, { error: "approver role required to decide an import row" });
    expect(
      (await approveRow(new Request("http://t/"), batchRow())).status,
    ).toBe(403);
  });

  test("reject forwards only the reason", async () => {
    const spy = upstream(200, { status: "rejected" });
    const res = await rejectRow(
      jsonReq(JSON.stringify({ reason: "not ours", status: "approved" })),
      batchRow(),
    );
    expect(res.status).toBe(200);
    const { url, init } = call(spy);
    expect(url).toBe(`${UP}/rows/${R}/reject`);
    expect(JSON.parse(String(init.body))).toEqual({ reason: "not ours" });
  });

  test("close forwards only the reason and passes a 409 through (#3002)", async () => {
    const spy = upstream(200, { batch_id: B, status: "closed" });
    const res = await closeBatch(
      jsonReq(JSON.stringify({ reason: "abandoned", status: "completed" })),
      batch(),
    );
    expect(res.status).toBe(200);
    const { url, init } = call(spy);
    expect(url).toBe(`${UP}/close`);
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ reason: "abandoned" });

    upstream(409, { error: "import batch is already completed or closed" });
    expect(
      (await closeBatch(jsonReq(JSON.stringify({ reason: "x" })), batch()))
        .status,
    ).toBe(409);
  });

  test("close refuses a non-UUID and a non-object body before upstream", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    expect(
      (
        await closeBatch(jsonReq("{}"), {
          params: Promise.resolve({ id: ".." }),
        })
      ).status,
    ).toBe(400);
    expect((await closeBatch(jsonReq("[1]"), batch())).status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("reject refuses a body that is not a JSON object", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await rejectRow(jsonReq("[1]"), batchRow());
    expect(res.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });
});
