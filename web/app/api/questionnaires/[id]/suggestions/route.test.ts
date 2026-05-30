// Slice 263 — vitest coverage for GET /api/questionnaires/[id]/suggestions.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

type FakeNextRequest = {
  nextUrl: { searchParams: URLSearchParams };
};

function makeReq(url: string): FakeNextRequest {
  const u = new URL(url, "http://test.local");
  return { nextUrl: { searchParams: u.searchParams } };
}

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/questionnaires/[id]/suggestions", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(
      makeReq("/api/questionnaires/q1/suggestions?anchor=IAC-06") as never,
      paramsFor("q1"),
    );
    expect(res.status).toBe(401);
  });

  test("forwards anchor query param + bearer", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          suggestions: [
            {
              ID: "sug-1",
              ScfAnchorID: "IAC-06",
              CanonicalText: "Yes — MFA enforced via Okta.",
              SourceLabel: "SIG Lite 2026 / Globex",
              UpdatedAt: "2026-02-14T00:00:00Z",
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await GET(
      makeReq("/api/questionnaires/q1/suggestions?anchor=IAC-06") as never,
      paramsFor("q1"),
    );
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires/q1/suggestions");
    expect(calledURL).toContain("anchor=IAC-06");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe(`Bearer ${TEST_BEARER_263}`);
  });

  test("propagates upstream 400 when anchor missing", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "anchor query param is required" }),
        { status: 400 },
      ),
    );
    const res = await GET(
      makeReq("/api/questionnaires/q1/suggestions") as never,
      paramsFor("q1"),
    );
    expect(res.status).toBe(400);
  });
});
