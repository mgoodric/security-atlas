// Slice 230 — vitest coverage for the dashboard export BFF.

import { beforeEach, describe, expect, test, vi } from "vitest";
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

function makeReq(query: string): Request {
  return new Request(`http://test/api/dashboard/export${query}`);
}

describe("GET /api/dashboard/export", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await GET(makeReq("?format=json"));
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("forwards bearer + query string and streams attachment headers", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ snapshot_at: "2026-07-25T00:00:00Z" }), {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Content-Disposition": `attachment; filename="dashboard_export_20260725.json"`,
          "X-Content-Type-Options": "nosniff",
        },
      }),
    );

    const query = "?format=json";
    const res = await GET(makeReq(query));

    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      `http://atlas:8080/v1/dashboard/export${query}`,
    );
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
    expect(res.headers.get("Content-Disposition")).toBe(
      `attachment; filename="dashboard_export_20260725.json"`,
    );
    expect(await res.json()).toEqual({
      snapshot_at: "2026-07-25T00:00:00Z",
    });
  });

  test("passes through backend errors and Retry-After", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "role does not grant dashboard/export access",
        }),
        {
          status: 403,
          headers: {
            "Content-Type": "application/json",
            "Retry-After": "30",
          },
        },
      ),
    );

    const res = await GET(makeReq("?format=xlsx"));

    expect(res.status).toBe(403);
    expect(res.headers.get("Retry-After")).toBe("30");
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/dashboard\/export/);
  });
});
