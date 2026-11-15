// #2137 — vitest coverage for the `/api/evidence/[id]` detail BFF.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing (no upstream call).
//   * Forwards the bearer to `GET /v1/evidence/{id}`, id URL-encoded,
//     with no query string and no tenant_id.
//   * Upstream body + status pass through (200 and the 404 the platform
//     returns for both unknown and inaccessible records).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET } from "./route";

const ID = "66666666-6666-6666-6666-66666666d137";

function call(id: string) {
  return GET(new Request(`http://localhost/api/evidence/${id}?tenant_id=x`), {
    params: Promise.resolve({ id }),
  });
}

describe("GET /api/evidence/[id]", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await call(ID);
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards the bearer and passes the record through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2137" });
    const upstream = {
      evidence: {
        evidence_id: ID,
        result: "pass",
        payload: { statement: "ok" },
      },
    };
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(upstream), { status: 200 }),
      );
    const res = await call(ID);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual(upstream);
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(`http://atlas:8080/v1/evidence/${ID}`);
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer-2137",
    );
  });

  test("encodes the id so it cannot change the upstream path", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2137" });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response("{}", { status: 404 }));
    await call("../controls");
    expect(fetchSpy.mock.calls[0][0]).toBe(
      "http://atlas:8080/v1/evidence/..%2Fcontrols",
    );
  });

  test("passes the upstream 404 through", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-2137" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "evidence not found" }), {
        status: 404,
      }),
    );
    const res = await call(ID);
    expect(res.status).toBe(404);
    expect(await res.json()).toEqual({ error: "evidence not found" });
  });
});
