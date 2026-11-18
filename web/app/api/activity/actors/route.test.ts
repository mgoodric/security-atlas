// #2414 — /api/activity/actors BFF: forwards the window and the
// read-telemetry toggle, and nothing else.

import { beforeEach, describe, expect, test, vi } from "vitest";
import type { NextRequest } from "next/server";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { GET } from "./route";

function req(query: string): NextRequest {
  const url = new URL(`http://localhost/api/activity/actors${query}`);
  return { nextUrl: url } as unknown as NextRequest;
}

describe("GET /api/activity/actors", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
  });

  test.each([
    ["", "http://atlas:8080/v1/activity/actors"],
    [
      "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&include_reads=true",
      "http://atlas:8080/v1/activity/actors?from=2026-01-01T00%3A00%3A00Z&to=2026-01-02T00%3A00%3A00Z&include_reads=true",
    ],
    [
      "?from=a&actor=system&kind=decision",
      "http://atlas:8080/v1/activity/actors?from=a",
    ],
  ])("query %j forwards to %s", async (query, want) => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response('{"actors":[]}', { status: 200 }));
    const res = await GET(req(query));
    expect(res.status).toBe(200);
    expect(String(fetchSpy.mock.calls[0][0])).toBe(want);
  });
});
