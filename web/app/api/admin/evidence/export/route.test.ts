// Slice 138 — vitest coverage for the evidence ledger metadata BFF
// route. Sibling of the vendors BFF test — same shape, evidence-
// specific assertion: the streamed body must NOT contain a `payload`
// or `payload_json` column header (slice 138 P0-A-Ledger-1).

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
  return new Request(`http://test/api/admin/evidence/export${query}`);
}

describe("GET /api/admin/evidence/export", () => {
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

  test("forwards bearer + query string verbatim and streams body without payload column", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    // Slice 138 P0-A-Ledger-1: payload column must NOT appear.
    const csvBody =
      "id,control_id,scope_id,evidence_query_id,observed_at,ingested_at,result,freshness_class,content_hash,payload_uri,valid_until,created_at\n" +
      `11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222,,,2026-05-01T00:00:00Z,2026-05-01T00:00:00Z,pass,fresh,sha256:abc,,,2026-05-01T00:00:00Z\n`;
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(csvBody, {
        status: 200,
        headers: {
          "Content-Type": "text/csv; charset=utf-8",
          "Content-Disposition": `attachment; filename="evidence_20260519.csv"`,
          "X-Content-Type-Options": "nosniff",
        },
      }),
    );

    const query = "?format=csv";
    const res = await GET(makeReq(query));
    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      `http://atlas:8080/v1/admin/evidence/export${query}`,
    );
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();

    const got = await res.text();
    expect(got).toBe(csvBody);
    // Slice 138 P0-A-Ledger-1 — payload column must be absent.
    expect(got).not.toMatch(/,payload[, \n]/);
    expect(got).not.toMatch(/,payload_json[, \n]/);
    // Required columns present.
    expect(got).toMatch(/content_hash/);
    expect(got).toMatch(/observed_at/);
    expect(got).toMatch(/freshness_class/);
  });

  test("passes through 400 from backend (bad format)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "unsupported format" }), {
        status: 400,
        headers: { "Content-Type": "application/json" },
      }),
    );
    expect((await GET(makeReq("?format=yaml"))).status).toBe(400);
  });

  test("passes through 403 from backend (no eligible role)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "forbidden" }), {
        status: 403,
        headers: { "Content-Type": "application/json" },
      }),
    );
    expect((await GET(makeReq("?format=csv"))).status).toBe(403);
  });

  test("passes through 429 from backend (concurrency cap)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "cap reached" }), {
        status: 429,
        headers: { "Content-Type": "application/json", "Retry-After": "30" },
      }),
    );
    expect((await GET(makeReq("?format=csv"))).status).toBe(429);
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
    expect((await GET(makeReq("?format=csv"))).status).toBe(200);
    const headers = fetchSpy.mock.calls[0][1]?.headers as
      | Record<string, string>
      | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-token");
    expect(headers?.Cookie).toBeUndefined();
  });
});
