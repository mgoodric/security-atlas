// Slice 138 — vitest coverage for the samples BFF route.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE, OIDC_SESSION_COOKIE } from "@/lib/auth";
import { GET } from "./route";

function makeReq(query: string): Request {
  return new Request(`http://test/api/admin/samples/export${query}`);
}

describe("GET /api/admin/samples/export", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    expect((await GET(makeReq("?format=csv"))).status).toBe(401);
  });

  test("forwards bearer + query and streams body containing audit_period_id column", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    const csvBody =
      "id,population_id,audit_period_id,control_id,n,seed,created_by,created_at,window_start,window_end,population_frozen_at,population_row_count\n";
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: { "Content-Type": "text/csv; charset=utf-8" },
      }),
    );
    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(200);
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      "http://atlas:8080/v1/admin/samples/export?format=csv",
    );
    const got = await res.text();
    // Slice 138 — samples export INCLUDES audit_period_id link.
    expect(got).toMatch(/audit_period_id/);
  });

  test("passes through 400 / 403 / 429 from backend", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    for (const status of [400, 403, 429]) {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        new Response(JSON.stringify({ error: "x" }), {
          status,
          headers: { "Content-Type": "application/json" },
        }),
      );
      expect((await GET(makeReq("?format=csv"))).status).toBe(status);
    }
  });

  test("ignores atlas_session cookie when present (slice 110 P0-A2)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    cookieStore.set(OIDC_SESSION_COOKIE, "test-atlas-session-id");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("ok", {
        status: 200,
        headers: { "Content-Type": "text/csv; charset=utf-8" },
      }),
    );
    await GET(makeReq("?format=csv"));
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Cookie).toBeUndefined();
  });
});
