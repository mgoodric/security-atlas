// Slice 757 — vitest coverage for POST /api/questionnaires/[id]/answers/[qid]/ai-reject.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { POST } from "./route";

function makeReq(body: unknown): { json: () => Promise<unknown> } {
  return { json: async () => body };
}

function paramsFor(
  id: string,
  qid: string,
): { params: Promise<{ id: string; qid: string }> } {
  return { params: Promise.resolve({ id, qid }) };
}

describe("POST /api/questionnaires/[id]/answers/[qid]/ai-reject", () => {
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
    const res = await POST(makeReq({}) as never, paramsFor("a", "b"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer + single answer_id to the upstream ai-reject", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          answer_id: "answer-1",
          question_id: "question-1",
          status: "rejected",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await POST(
      makeReq({ answer_id: "answer-1" }) as never,
      paramsFor("qn1", "question-1"),
    );
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(
      "/v1/questionnaires/qn1/answers/question-1/ai-reject",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
    const auth = (init?.headers as Record<string, string>)?.Authorization;
    expect(auth).toBe(`Bearer ${TEST_BEARER_263}`);
    expect(String(init?.body ?? "")).toContain('"answer_id":"answer-1"');
  });

  test("propagates upstream 409 verbatim (approved/manual target)", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "answer is approved and cannot be rejected" }),
        { status: 409, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await POST(
      makeReq({ answer_id: "answer-1" }) as never,
      paramsFor("qn1", "question-1"),
    );
    expect(res.status).toBe(409);
  });

  test("propagates upstream 404 verbatim (absent/cross-tenant target)", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "ai-suggested answer not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST(
      makeReq({ answer_id: "answer-1" }) as never,
      paramsFor("qn1", "question-1"),
    );
    expect(res.status).toBe(404);
  });
});
