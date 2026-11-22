// #2848 — vitest coverage for the risk import upload, preview and value
// mapping BFF routes: each forwards the bearer to its /v1/risks/imports
// path and passes the platform's status and body through. The upload body
// (file, methodology, column_map, ignore_column) is forwarded as sent, and
// the value body is allow-listed.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../../lib/test-utils/test-tokens";
import { multipartRequest } from "../../../../lib/test-utils/upload-requests";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { PUT as mapValue } from "./[id]/values/route";
import { POST as preview } from "./preview/route";
import { POST as upload } from "./route";

const B = "b2848000-0000-4000-8000-000000000001";

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

function form(): FormData {
  const f = new FormData();
  f.append("file", new File(["Headline,Bucket\nA,B\n"], "r.csv"));
  f.append("methodology", "fair");
  f.append("column_map", "title=Headline");
  f.append("column_map", "category=Bucket");
  f.append("ignore_column", "Notes");
  return f;
}

describe("risk import upload BFF routes", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  for (const [name, route, path, status] of [
    ["upload", upload, "/v1/risks/imports", 201],
    ["preview", preview, "/v1/risks/imports/preview", 200],
  ] as const) {
    test(`${name} forwards the multipart mapping fields`, async () => {
      const spy = upstream(status, { ok: true });
      const res = await route((await multipartRequest(form())) as never);
      expect(res.status).toBe(status);
      const { url, init } = call(spy);
      expect(url).toBe(`http://atlas:8080${path}`);
      expect(init.method).toBe("POST");
      expect((init.headers as Record<string, string>).Authorization).toBe(
        `Bearer ${TEST_BEARER_DEFAULT}`,
      );
      const sent = init.body as FormData;
      expect(sent.getAll("column_map")).toEqual([
        "title=Headline",
        "category=Bucket",
      ]);
      expect(sent.getAll("ignore_column")).toEqual(["Notes"]);
      expect(sent.get("methodology")).toBe("fair");
    });

    test(`${name} passes a platform 400 through`, async () => {
      upstream(400, { error: "invalid column map" });
      const res = await route((await multipartRequest(form())) as never);
      expect(res.status).toBe(400);
      expect(await res.json()).toEqual({ error: "invalid column map" });
    });

    test(`${name} refuses a body that is not multipart`, async () => {
      const spy = vi.spyOn(globalThis, "fetch");
      const res = await route(
        new Request("http://t/", {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "content-length": "2",
          },
          body: "{}",
        }) as never,
      );
      expect(res.status).toBe(400);
      expect(spy).not.toHaveBeenCalled();
    });
  }

  test("value mapping forwards only the platform's fields", async () => {
    const spy = upstream(200, {
      kind: "category",
      value: "Ops",
      target: "operational",
    });
    const req = new Request("http://t/", {
      method: "PUT",
      body: JSON.stringify({
        kind: "category",
        value: "Ops",
        create: { slug: "ops", name: "Ops", description: "", extra: 1 },
        tenant_id: "x",
      }),
    });
    const res = await mapValue(req, { params: Promise.resolve({ id: B }) });
    expect(res.status).toBe(200);
    const { url, init } = call(spy);
    expect(url).toBe(`http://atlas:8080/v1/risks/imports/${B}/values`);
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({
      kind: "category",
      value: "Ops",
      create: { slug: "ops", name: "Ops", description: "" },
    });
  });

  test("value mapping refuses a non-UUID and a non-object body", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const bad = await mapValue(
      new Request("http://t/", { method: "PUT", body: "{}" }),
      { params: Promise.resolve({ id: ".." }) },
    );
    expect(bad.status).toBe(400);
    const arr = await mapValue(
      new Request("http://t/", { method: "PUT", body: "[]" }),
      { params: Promise.resolve({ id: B }) },
    );
    expect(arr.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });
});
