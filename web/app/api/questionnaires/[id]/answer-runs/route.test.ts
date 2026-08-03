// Slice 757 — vitest coverage for POST /api/questionnaires/[id]/answer-runs
// (starts a slice-756 batch answer-drafting run).

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

import { POST } from "./route";

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("POST /api/questionnaires/[id]/answer-runs", () => {
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
    const res = await POST({} as never, paramsFor("qn1"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer to the upstream start endpoint", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ run: { id: "run-1" }, items: [] }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await POST({} as never, paramsFor("qn1"));
    expect(res.status).toBe(201);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires/qn1/answer-runs");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
    const auth = (init?.headers as Record<string, string>)?.Authorization;
    expect(auth).toBe(`Bearer ${TEST_BEARER_263}`);
  });

  test("propagates upstream 409 verbatim (a run is already active)", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "questionnaire already has an active answer run",
        }),
        { status: 409, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await POST({} as never, paramsFor("qn1"));
    expect(res.status).toBe(409);
  });
});
