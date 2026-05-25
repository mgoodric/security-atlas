// Slice 263 — vitest coverage for PATCH /api/questionnaires/[id]/answers/[qid].

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

import { PATCH } from "./route";

function makeReq(body: unknown): { json: () => Promise<unknown> } {
  return { json: async () => body };
}

function paramsFor(
  id: string,
  qid: string,
): { params: Promise<{ id: string; qid: string }> } {
  return { params: Promise.resolve({ id, qid }) };
}

describe("PATCH /api/questionnaires/[id]/answers/[qid]", () => {
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
    const res = await PATCH(makeReq({}) as never, paramsFor("a", "b"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer + json body to upstream PATCH", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-263" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          id: "ans-1",
          answer_value: "Yes",
          narrative: "MFA enforced.",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await PATCH(
      makeReq({
        answer_value: "Yes",
        narrative: "MFA enforced.",
        save_to_library: true,
      }) as never,
      paramsFor("q1", "q1-question"),
    );
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires/q1/answers/q1-question");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("PATCH");
    const sent = String(init?.body ?? "");
    expect(sent).toContain('"answer_value":"Yes"');
    expect(sent).toContain('"save_to_library":true');
  });

  test("propagates upstream 500 verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-263" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "boom" }), { status: 500 }),
    );
    const res = await PATCH(makeReq({}) as never, paramsFor("a", "b"));
    expect(res.status).toBe(500);
  });
});
