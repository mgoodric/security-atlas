import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { POST } from "./route";

function paramsFor(
  id: string,
  qid: string,
): { params: Promise<{ id: string; qid: string }> } {
  return { params: Promise.resolve({ id, qid }) };
}

describe("POST mapping ai-suggest BFF", () => {
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
    const res = await POST({} as never, paramsFor("q1", "question1"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer to upstream mapping suggest route", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ scf_anchor_id: "IAC-06" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST({} as never, paramsFor("q1", "question1"));
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(
      "/v1/questionnaires/q1/questions/question1/scf-mapping/ai-suggest",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe(`Bearer ${TEST_BEARER_263}`);
  });
});
