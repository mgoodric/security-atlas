// Slice 263 — vitest coverage for web/app/api/questionnaires/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing (GET + POST)
//   * Forwards the bearer as `Authorization: Bearer <token>` upstream
//   * GET hits `/v1/questionnaires` and passes the upstream body through
//   * POST forwards the request body as JSON to `/v1/questionnaires`
//   * Upstream error status passes through as the response status
//
// All test bearers use the neutral `test-bearer-263` token — NO vendor
// token prefixes (per slice 098 P0-A5 + GitGuardian discipline).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { GET, POST } from "./route";

type FakeReq = { json: () => Promise<unknown> };

function makePostReq(body: unknown): FakeReq {
  return { json: async () => body };
}

describe("GET /api/questionnaires", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer + returns upstream list on success", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          questionnaires: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              name: "CAIQ v4.1",
              source_label: "CAIQ",
              source_filename: "caiq.xlsx",
              status: "draft",
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      questionnaires: { name: string }[];
    };
    expect(body.questionnaires).toHaveLength(1);
    expect(body.questionnaires[0].name).toBe("CAIQ v4.1");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe(`Bearer ${TEST_BEARER_263}`);
  });

  test("propagates upstream error status on GET", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );
    const res = await GET();
    expect(res.status).toBe(502);
  });
});

describe("POST /api/questionnaires", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    delete process.env.ATLAS_HTTP_URL;
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await POST(makePostReq({ name: "x" }) as never);
    expect(res.status).toBe(401);
  });

  test("forwards body and bearer on POST", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          id: "00000000-0000-0000-0000-000000000002",
          name: "New",
          status: "draft",
        }),
        { status: 201, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await POST(
      makePostReq({ name: "New", source_filename: "a.xlsx" }) as never,
    );
    expect(res.status).toBe(201);
    expect(fetchSpy).toHaveBeenCalledOnce();
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
    const bodyStr = String(init?.body ?? "");
    expect(bodyStr).toContain("New");
    expect(bodyStr).toContain("a.xlsx");
  });

  test("propagates upstream error status on POST", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 500 }),
    );
    const res = await POST(makePostReq({ name: "x" }) as never);
    expect(res.status).toBe(500);
  });
});
