// #1817 — vitest coverage for web/app/api/risks/categories/route.ts.
//
//   * 401 when the bearer cookie is missing.
//   * forwards the bearer to `/v1/risks/categories` and passes the
//     upstream body through.
//   * forwards `include_archived` and nothing else.
//   * upstream error status passes through.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

function req(qs = ""): Request {
  return new Request(`http://localhost/api/risks/categories${qs}`);
}

describe("GET /api/risks/categories", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(req());
    expect(res.status).toBe(401);
  });

  test("forwards bearer + returns the upstream categories", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-1817" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          categories: [{ slug: "operational", name: "Operational" }],
          count: 1,
        }),
        { status: 200 },
      ),
    );

    const res = await GET(req());
    expect(res.status).toBe(200);
    const body = (await res.json()) as { categories: { slug: string }[] };
    expect(body.categories[0]?.slug).toBe("operational");

    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toBe("http://atlas:8080/v1/risks/categories");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-1817");
  });

  test("forwards include_archived only", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-1817" });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ categories: [], count: 0 })),
      );
    await GET(req("?include_archived=true&other=x"));
    expect(String(fetchSpy.mock.calls[0]?.[0] ?? "")).toBe(
      "http://atlas:8080/v1/risks/categories?include_archived=true",
    );
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-1817" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "bad" }), { status: 400 }),
    );
    const res = await GET(req("?include_archived=maybe"));
    expect(res.status).toBe(400);
  });
});
