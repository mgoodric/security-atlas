// Slice 135 — vitest coverage for the /api/audit-log/export BFF route.
//
// AC-13. Coverage matrix:
//
//   * No `sa_session_token` (bearer) cookie -> 401 { error }.
//   * Bearer present, query string forwarded verbatim to the
//     upstream /v1/admin/audit-log/export path; bearer attached as
//     Authorization: Bearer ...
//   * Backend 200 happy path -> stream the body + Content-Type +
//     Content-Disposition headers through verbatim.
//   * Backend 400 / 403 / 413 -> pass through the JSON error body
//     and status unchanged.
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
  return new Request(`http://test/api/audit-log/export${query}`);
}

describe("GET /api/audit-log/export", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await GET(
      makeReq("?format=csv&from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBeDefined();
  });

  test("forwards bearer + query string verbatim on happy path", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    const csvBody =
      "occurred_at,actor_id,kind\n2026-05-17T00:00:00Z,user-1,decision\n";
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: {
          "Content-Type": "text/csv; charset=utf-8",
          "Content-Disposition": `attachment; filename="audit-log_20260518.csv"`,
          "X-Content-Type-Options": "nosniff",
        },
      }),
    );

    const query =
      "?format=csv&from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z&kind=evidence,me";
    const res = await GET(makeReq(query));
    expect(res.status).toBe(200);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0];
    const requestedURL = String(call[0]);
    expect(requestedURL).toBe(
      `http://atlas:8080/v1/admin/audit-log/export${query}`,
    );
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    // Slice 110 P0-A2: cookie MUST NOT be forwarded.
    expect(headers?.Cookie).toBeUndefined();

    // Body + Content-Type + Content-Disposition flow through.
    expect(res.headers.get("Content-Type")).toBe("text/csv; charset=utf-8");
    expect(res.headers.get("Content-Disposition")).toBe(
      `attachment; filename="audit-log_20260518.csv"`,
    );
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
    const gotBody = await res.text();
    expect(gotBody).toBe(csvBody);
  });

  test("passes through 400 from backend (missing from)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "from query parameter is required (RFC3339)" }),
        { status: 400, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/from query parameter is required/);
  });

  test("passes through 403 from backend (no eligible role)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "admin, auditor, or grc_engineer role required",
        }),
        { status: 403, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      makeReq("?format=csv&from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/admin, auditor/);
  });

  test("passes through 413 from backend (row cap exceeded)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error:
            "export would exceed row cap of 100000; narrow the request filter (from/to window, kind=, actor=) and retry",
        }),
        { status: 413, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(
      makeReq("?format=csv&from=2026-02-11T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(413);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/row cap/);
    expect(body.error).toMatch(/narrow/);
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

    const res = await GET(
      makeReq("?format=csv&from=2026-05-11T00:00:00Z&to=2026-05-18T00:00:00Z"),
    );
    expect(res.status).toBe(200);
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
  });
});
