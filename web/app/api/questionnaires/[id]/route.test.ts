// Slice 263 — vitest coverage for `GET /api/questionnaires/{id}`.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("next/server", () => {
  class NextResponse extends Response {
    static json(
      body: unknown,
      init?: { status?: number; headers?: Record<string, string> },
    ): NextResponse {
      return new NextResponse(JSON.stringify(body), {
        status: init?.status ?? 200,
        headers: {
          "Content-Type": "application/json",
          ...(init?.headers ?? {}),
        },
      });
    }
  }
  return { NextResponse };
});

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

function paramsFor(id: string): {
  params: Promise<{ id: string }>;
} {
  return { params: Promise.resolve({ id }) };
}

describe("GET /api/questionnaires/[id]", () => {
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
    const res = await GET({} as never, paramsFor("abc"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer + encodes id into upstream URL", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-263" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          questionnaire: {
            id: "abc",
            name: "CAIQ",
            status: "draft",
          },
          questions: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET({} as never, paramsFor("abc"));
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires/abc");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-263");
  });

  test("propagates upstream 404", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-263" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "not found" }), { status: 404 }),
    );
    const res = await GET({} as never, paramsFor("missing"));
    expect(res.status).toBe(404);
  });
});
