// #2135 — vitest coverage for web/app/api/needs-attention/route.ts.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../lib/test-utils/next-mocks";
import { TEST_BEARER_094 } from "../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET } from "./route";

const body = {
  for: "me",
  items: [],
  total: 0,
  counts: {},
  type_counts: {},
  limit: 10,
  offset: 0,
};

describe("GET /api/needs-attention", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when the bearer cookie is missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(new Request("http://localhost/api/needs-attention"));
    expect(res.status).toBe(401);
  });

  test("forwards only for, type, limit and offset, with the bearer", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_094 });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(body), { status: 200 }),
      );
    const res = await GET(
      new Request(
        "http://localhost/api/needs-attention?for=all&type=control,risk&limit=25&offset=25&junk=1",
      ),
    );
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual(body);
    const url = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(url).toBe(
      "http://atlas:8080/v1/needs-attention?for=all&type=control%2Crisk&limit=25&offset=25",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe(`Bearer ${TEST_BEARER_094}`);
  });

  test("passes the platform's 403 through (for=all refused)", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_094 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("{}", { status: 403, statusText: "Forbidden" }),
    );
    const res = await GET(
      new Request("http://localhost/api/needs-attention?for=all"),
    );
    expect(res.status).toBe(403);
  });
});
