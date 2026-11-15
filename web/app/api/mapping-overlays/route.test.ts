// #1802 — vitest coverage for the overlay BFF routes: the audited-under
// read and its CSV export forward the bearer and only the framework
// filter; the overlay POST forwards an allow-listed body and passes the
// platform's refusals through.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET as exportReport } from "./audited-under/export/route";
import { GET as getReport } from "./audited-under/route";
import { POST as addEdge } from "./route";

function upstream(status: number, body: string, headers = {}) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(body, {
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

describe("mapping overlay BFF routes", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  test("401 without a bearer cookie", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const req = new Request("http://t/");
    expect((await getReport(req)).status).toBe(401);
    expect((await exportReport(req)).status).toBe(401);
    expect((await addEdge(jsonReq("{}"))).status).toBe(401);
  });

  test("the report forwards only the framework filter, uncached", async () => {
    const spy = upstream(200, JSON.stringify({ rows: [], count: 0 }));
    const res = await getReport(
      new Request("http://t/?framework=soc2&x=1&framework=iso"),
    );
    expect(res.status).toBe(200);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    const { url, init } = call(spy);
    expect(url).toBe(
      "http://atlas:8080/v1/mapping-overlays/audited-under?framework=soc2",
    );
    expect((init.headers as Record<string, string>).Authorization).toBe(
      `Bearer ${TEST_BEARER_DEFAULT}`,
    );
    expect(init.cache).toBe("no-store");
  });

  test("the report sends no query without a filter", async () => {
    const spy = upstream(200, JSON.stringify({ rows: [], count: 0 }));
    await getReport(new Request("http://t/"));
    expect(call(spy).url).toBe(
      "http://atlas:8080/v1/mapping-overlays/audited-under",
    );
  });

  test("the export streams the CSV with the platform's filename", async () => {
    const spy = upstream(200, "control_id,framework\n", {
      "Content-Type": "text/csv; charset=utf-8",
      "Content-Disposition": 'attachment; filename="audited-under.csv"',
    });
    const res = await exportReport(new Request("http://t/?framework=a%26b"));
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("text/csv; charset=utf-8");
    expect(res.headers.get("content-disposition")).toBe(
      'attachment; filename="audited-under.csv"',
    );
    expect(await res.text()).toBe("control_id,framework\n");
    expect(call(spy).url).toBe(
      "http://atlas:8080/v1/mapping-overlays/audited-under/export?framework=a%26b",
    );
  });

  test("an export error passes through as JSON", async () => {
    upstream(500, JSON.stringify({ error: "boom" }));
    const res = await exportReport(new Request("http://t/"));
    expect(res.status).toBe(500);
    expect(await res.json()).toEqual({ error: "boom" });
  });

  test("the overlay POST forwards only the overlay fields", async () => {
    const spy = upstream(201, JSON.stringify({ overlay_edge: {} }));
    const res = await addEdge(
      jsonReq(
        JSON.stringify({
          framework: "soc2",
          framework_version: "2017",
          requirement_code: "CC6.1",
          scf_release: "2026.3",
          anchor_code: "IAC-06",
          relationship_type: "intersects_with",
          strength: 0.5,
          rationale: "Audit relied on it",
          source_ref: "FY25 report s4",
          tenant_id: "00000000-0000-4000-8000-000000000001",
          created_by: "someone",
        }),
      ),
    );
    expect(res.status).toBe(201);
    const { url, init } = call(spy);
    expect(url).toBe("http://atlas:8080/v1/mapping-overlays");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      framework: "soc2",
      framework_version: "2017",
      requirement_code: "CC6.1",
      scf_release: "2026.3",
      anchor_code: "IAC-06",
      relationship_type: "intersects_with",
      strength: 0.5,
      rationale: "Audit relied on it",
      source_ref: "FY25 report s4",
    });
  });

  test("the overlay POST refuses a non-object body before calling upstream", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    expect((await addEdge(jsonReq("[1]"))).status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("a platform refusal passes through", async () => {
    upstream(403, JSON.stringify({ error: "forbidden" }));
    const res = await addEdge(jsonReq(JSON.stringify({ framework: "x" })));
    expect(res.status).toBe(403);
    expect(await res.json()).toEqual({ error: "forbidden" });
  });
});
