import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../../../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { POST } from "./route";

function paramsFor(): {
  params: Promise<{ id: string; qid: string; proposalID: string }>;
} {
  return {
    params: Promise.resolve({
      id: "q1",
      qid: "question1",
      proposalID: "proposal1",
    }),
  };
}

describe("POST mapping approve BFF", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("forwards approve route", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ scf_anchor_id: "IAC-06" }), {
        status: 200,
      }),
    );
    const res = await POST({} as never, paramsFor());
    expect(res.status).toBe(200);
    expect(String(fetchSpy.mock.calls[0]?.[0] ?? "")).toContain(
      "/v1/questionnaires/q1/questions/question1/scf-mapping/proposal1/approve",
    );
  });
});
