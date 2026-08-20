// Slice 757 — vitest coverage for GET /api/questionnaires/[id]/answer-runs/[runId]
// (slice-756 run status + per-item outcomes for the review queue).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET } from "./route";

function paramsFor(
  id: string,
  runId: string,
): { params: Promise<{ id: string; runId: string }> } {
  return { params: Promise.resolve({ id, runId }) };
}

describe("GET /api/questionnaires/[id]/answer-runs/[runId]", () => {
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
    const res = await GET({} as never, paramsFor("qn1", "run-1"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer to the upstream run-status endpoint", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          run: { id: "run-1", status: "completed", drafted_count: 3 },
          items: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await GET({} as never, paramsFor("qn1", "run-1"));
    expect(res.status).toBe(200);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires/qn1/answer-runs/run-1");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const auth = (init?.headers as Record<string, string>)?.Authorization;
    expect(auth).toBe(`Bearer ${TEST_BEARER_263}`);
  });

  test("propagates upstream 404 verbatim", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "answer run not found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await GET({} as never, paramsFor("qn1", "run-x"));
    expect(res.status).toBe(404);
  });
});
