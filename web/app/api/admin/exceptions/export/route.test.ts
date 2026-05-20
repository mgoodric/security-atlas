// Slice 138 — vitest coverage for the exceptions BFF route.

import { beforeEach, describe, expect, test, vi } from "vitest";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => {
  class NextResponse extends Response {
    static json(
      body: unknown,
      init?: { status?: number; headers?: Record<string, string> },
    ): NextResponse {
      return new NextResponse(body === null ? "null" : JSON.stringify(body), {
        status: init?.status ?? 200,
        headers: {
          "Content-Type": "application/json",
          ...(init?.headers ?? {}),
        },
      });
    }
  }
  return { NextResponse };
});

import { SESSION_COOKIE, OIDC_SESSION_COOKIE } from "@/lib/auth";
import { GET } from "./route";

function makeReq(query: string): Request {
  return new Request(`http://test/api/admin/exceptions/export${query}`);
}

describe("GET /api/admin/exceptions/export", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    expect((await GET(makeReq("?format=csv"))).status).toBe(401);
  });

  test("forwards bearer + query and streams body containing duration + justification columns", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    // Slice 138 — exceptions export INCLUDES owner (requested_by) +
    // duration + justification per slice doc.
    const csvBody =
      "id,control_id,status,justification,compensating_controls,scope_cell_predicate,requested_by,requested_at,approved_by,approved_at,denied_by,denied_at,activated_by,activated_at,effective_from,expires_at,expired_at,duration_days,created_at,updated_at\n";
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: { "Content-Type": "text/csv; charset=utf-8" },
      }),
    );
    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(200);
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      "http://atlas:8080/v1/admin/exceptions/export?format=csv",
    );
    const got = await res.text();
    expect(got).toMatch(/justification/);
    expect(got).toMatch(/duration_days/);
    expect(got).toMatch(/requested_by/);
  });

  test("passes through 400 / 403 / 429 from backend", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
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
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
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
