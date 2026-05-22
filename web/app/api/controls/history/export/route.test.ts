// Slice 175 — vitest coverage for the /api/controls/history/export BFF route.
//
// Coverage matrix mirrors the slice 137 `/api/controls/export` tests:
//
//   * No `SESSION_COOKIE` (post-slice-206: `atlas_jwt`) cookie -> 401 { error }.
//   * Bearer present, query string forwarded verbatim to the upstream
//     /v1/controls/history/export path; bearer attached as
//     Authorization: Bearer ...
//   * Backend 200 happy path -> stream the body + Content-Type +
//     Content-Disposition headers through verbatim.
//   * Backend 400 / 403 -> pass through the JSON error body and
//     status unchanged.
//   * Backend 429 (concurrency cap) -> pass through Retry-After
//     header alongside the JSON body (slice 145 inherited).
//   * Slice 110 P0-A2: the atlas_session cookie MUST NOT be forwarded
//     even when present in the jar.

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
  return new Request(`http://test/api/controls/history/export${query}`);
}

describe("GET /api/controls/history/export", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBeDefined();
  });

  test("forwards bearer + query string verbatim on happy path", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    const csvBody =
      "id,bundle_id,version,title,...,superseded_by,superseded_at\n" +
      "11111111-1111-1111-1111-111111111111,bundle-x,2,test,...,,\n";
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: {
          "Content-Type": "text/csv; charset=utf-8",
          "Content-Disposition": `attachment; filename="controls_history_20260522.csv"`,
          "X-Content-Type-Options": "nosniff",
        },
      }),
    );

    const query = "?format=csv";
    const res = await GET(makeReq(query));
    expect(res.status).toBe(200);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0];
    const requestedURL = String(call[0]);
    expect(requestedURL).toBe(
      `http://atlas:8080/v1/controls/history/export${query}`,
    );
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    // Slice 110 P0-A2: cookie MUST NOT be forwarded.
    expect(headers?.Cookie).toBeUndefined();

    // Body + Content-Type + Content-Disposition flow through.
    expect(res.headers.get("Content-Type")).toBe("text/csv; charset=utf-8");
    expect(res.headers.get("Content-Disposition")).toBe(
      `attachment; filename="controls_history_20260522.csv"`,
    );
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
    const gotBody = await res.text();
    expect(gotBody).toBe(csvBody);
  });

  test("passes through 400 from backend (unsupported format)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: `unsupported format "pdf" (want csv|json|xlsx)`,
        }),
        { status: 400, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(makeReq("?format=pdf"));
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/unsupported format/);
  });

  test("passes through 403 from backend (no eligible role)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "role does not grant controls/program-read access",
        }),
        { status: 403, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/program-read/);
  });

  test("passes through 429 + Retry-After header from backend (concurrency cap)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "export concurrency cap (2) reached for this (tenant, user)",
          retry_after_seconds: 30,
          cap: 2,
        }),
        {
          status: 429,
          headers: {
            "Content-Type": "application/json",
            "Retry-After": "30",
          },
        },
      ),
    );

    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(429);
    expect(res.headers.get("Retry-After")).toBe("30");
    const body = (await res.json()) as { error: string; cap: number };
    expect(body.error).toMatch(/concurrency cap/);
    expect(body.cap).toBe(2);
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

    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(200);
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
  });
});
