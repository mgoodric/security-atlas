// Slice 094 — vitest coverage for app/api/calendar/subscription/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing (defense in depth — the
//     middleware would also reject, but the BFF should not POST upstream
//     when it knows the user is unauthenticated).
//   * Calls upstream POST /v1/calendar/subscription with the bearer.
//   * Returns 201 + URL body on success.
//
// Test fixtures use neutral strings only — NO vendor token prefixes.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_094 } from "../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();
vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST } from "./route";

describe("POST /api/calendar/subscription", () => {
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
    const res = await POST();
    expect(res.status).toBe(401);
  });

  test("forwards POST to upstream and returns 201 with URL", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_094 });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          url: "/v1/calendar.ics?token=test-token-abcdef",
          expires_at: "2027-05-16T12:00:00Z",
        }),
        { status: 201, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await POST();
    expect(res.status).toBe(201);
    const body = await res.json();
    expect(body.url).toBe("/v1/calendar.ics?token=test-token-abcdef");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/calendar/subscription");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe(`Bearer ${TEST_BEARER_094}`);
  });
});
