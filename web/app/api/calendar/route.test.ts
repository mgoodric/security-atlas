// Slice 094 — vitest coverage for web/app/api/calendar/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing.
//   * Forwards query params (from / to / types) to /v1/calendar.
//   * Upstream JSON body passes through verbatim on success.
//   * Upstream error status passes through as the response status.
//
// Test fixtures use neutral strings only — NO vendor token prefixes.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../lib/test-utils/next-mocks";
import { TEST_BEARER_094 } from "../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { linkFor } from "../../../components/calendar/link-for";
import type { CalendarResponse } from "../../../lib/api/calendar";

import { GET } from "./route";

describe("GET /api/calendar", () => {
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
    const res = await GET(new Request("http://localhost/api/calendar"));
    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards query params and returns upstream JSON on success", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_094 });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          events: [],
          count: 0,
          from: "2026-05-16T00:00:00Z",
          to: "2026-08-14T00:00:00Z",
          truncated: false,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      new Request(
        "http://localhost/api/calendar?from=2026-06-01&to=2026-06-30&types=audit,policy",
      ),
    );
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.count).toBe(0);

    // Confirm we forwarded all three query params to the upstream URL.
    expect(fetchSpy).toHaveBeenCalledOnce();
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("from=2026-06-01");
    expect(calledURL).toContain("to=2026-06-30");
    expect(calledURL).toContain("types=audit%2Cpolicy");
    // Bearer header propagated.
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe(`Bearer ${TEST_BEARER_094}`);
  });

  test("passes an offboarding event through, and linkFor links it (#2215)", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_094 });
    const offboarding = {
      id: "psc-1",
      type: "offboarding",
      title: "Offboarding access removal: departing@example.com",
      starts_at: "2026-06-03T00:00:00Z",
      related_entity_id: "psc-1",
      related_entity_kind: "personnel_security_checklist",
      summary: "manual",
      status: "upcoming",
    };
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          events: [offboarding],
          count: 1,
          from: "2026-06-01T00:00:00Z",
          to: "2026-06-30T00:00:00Z",
          truncated: false,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      new Request("http://localhost/api/calendar?types=offboarding"),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as CalendarResponse;
    expect(body.events).toEqual([offboarding]);
    expect(body.events.map(linkFor)).toEqual([
      { kind: "link", href: "/personnel-security/psc-1" },
    ]);
  });

  test("propagates upstream error status", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_094 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("server error", { status: 502 }),
    );

    const res = await GET(new Request("http://localhost/api/calendar"));
    expect(res.status).toBe(502);
  });
});
