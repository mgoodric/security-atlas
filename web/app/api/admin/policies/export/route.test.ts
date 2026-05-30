// Slice 138 — vitest coverage for the policies BFF route.

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
  return new Request(`http://test/api/admin/policies/export${query}`);
}

describe("GET /api/admin/policies/export", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(401);
  });

  test("forwards bearer + query string verbatim on happy path", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    const csvBody =
      "id,title,version,status,effective_date,owner,approver,acknowledgment_required_role,next_review_at,body_md,created_at,updated_at\n";
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: { "Content-Type": "text/csv; charset=utf-8" },
      }),
    );
    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(200);
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      "http://atlas:8080/v1/admin/policies/export?format=csv",
    );
    expect(await res.text()).toBe(csvBody);
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
