// Slice 139 — vitest coverage for the audit-periods export BFF route.
//
// AC-3/4 coverage matrix (mirrors slice 135 audit-log/export tests):
//
//   * No `ATLAS_JWT_COOKIE` (post-slice-206: `atlas_jwt`) cookie  -> 401 { error }
//   * Bearer present                 -> happy path: stream body + headers
//   * Backend 400 / 403 / 413 / 429  -> JSON error body + status passthrough
//   * Slice 110 P0-A2: atlas_session cookie MUST NOT be forwarded

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
  return new Request(`http://test/api/admin/audit-periods/export${query}`);
}

describe("GET /api/admin/audit-periods/export", () => {
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
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    const csvBody =
      "id,name,framework_version_id,period_start,period_end,status,frozen_at,frozen_by,frozen_hash,created_by,created_at,updated_at\n" +
      "11111111-1111-1111-1111-111111111111,Q1,22222222-2222-2222-2222-222222222222,2026-01-01,2026-03-31,frozen,2026-04-01T00:00:00Z,alice,deadbeef,bob,2026-01-01T00:00:00Z,2026-04-01T00:00:00Z\n";
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: {
          "Content-Type": "text/csv; charset=utf-8",
          "Content-Disposition": `attachment; filename="audit-periods_20260519.csv"`,
          "X-Content-Type-Options": "nosniff",
        },
      }),
    );

    const query = "?format=csv";
    const res = await GET(makeReq(query));
    expect(res.status).toBe(200);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0];
    expect(String(call[0])).toBe(
      `http://atlas:8080/v1/admin/audit-periods/export${query}`,
    );
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();

    expect(res.headers.get("Content-Type")).toBe("text/csv; charset=utf-8");
    expect(res.headers.get("Content-Disposition")).toBe(
      `attachment; filename="audit-periods_20260519.csv"`,
    );
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
    expect(await res.text()).toBe(csvBody);
  });

  test("passes through 400 from backend (bad format)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
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
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "admin, auditor, or grc_engineer role required",
        }),
        { status: 403, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/admin, auditor/);
  });

  test("passes through 429 from backend (concurrency cap)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error:
            "export concurrency cap (2) reached for this (tenant, user); retry in 30s",
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
    const body = (await res.json()) as { error: string };
    expect(body.error).toMatch(/concurrency cap/);
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

    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(200);
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
  });
});
