// Slice 139 — vitest coverage for the vendor export BFF route.
//
// Sibling of the audit-periods BFF test. Same matrix:
//
//   * No `sa_session_token` cookie  -> 401 { error }
//   * Bearer present                 -> happy path: stream body + headers
//   * Backend 400 / 403 / 413 / 429  -> JSON error body + status passthrough
//   * Slice 110 P0-A2: atlas_session cookie MUST NOT be forwarded
//
// Plus one vendor-specific assertion: the happy-path body contains the
// `owner_user_masked` column header, not the un-masked `owner_user`
// column. The BFF only forwards bytes, so this is really an
// integration assertion against the canonical body shape — but
// checking it here catches a regression where the upstream column set
// drifts from the masked-only contract.

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
  return new Request(`http://test/api/admin/vendors/export${query}`);
}

describe("GET /api/admin/vendors/export", () => {
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
    // Slice 139 D1: owner_user_masked column. Un-masked owner_user
    // must NOT appear in the canonical header.
    const csvBody =
      "id,name,domain,criticality,contract_start,contract_end,dpa_signed,dpa_signed_at,review_cadence,last_review_date,overdue,owner_user_masked,linked_sow_uri,notes,scope_cell_ids,created_at,updated_at\n" +
      `11111111-1111-1111-1111-111111111111,Datadog,datadoghq.com,high,2025-01-01,2026-01-01,true,2025-01-15,annual,2026-04-01,false,*@example.com,,obs,,2026-04-01T00:00:00Z,2026-04-01T00:00:00Z\n`;
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: {
          "Content-Type": "text/csv; charset=utf-8",
          "Content-Disposition": `attachment; filename="vendors_20260519.csv"`,
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
      `http://atlas:8080/v1/admin/vendors/export${query}`,
    );
    const headers = call[1]?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();

    const got = await res.text();
    expect(got).toBe(csvBody);
    // Slice 139 D1 — the canonical column is owner_user_masked, not
    // owner_user. If the upstream column set drifts, this assertion
    // fails. (The BFF doesn't validate; this test pins the contract.)
    expect(got).toMatch(/owner_user_masked/);
    expect(got).toMatch(/\*@example\.com/);
  });

  test("passes through 400 from backend (bad format)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: `unsupported format "yaml" (want csv|json|xlsx)`,
        }),
        { status: 400, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await GET(makeReq("?format=yaml"));
    expect(res.status).toBe(400);
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
    const res = await GET(makeReq("?format=csv"));
    expect(res.status).toBe(403);
  });

  test("passes through 429 from backend (concurrency cap)", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "export concurrency cap (2) reached",
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
