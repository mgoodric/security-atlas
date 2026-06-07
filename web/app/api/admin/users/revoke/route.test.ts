// Slice 479 — vitest coverage for web/app/api/admin/users/revoke/route.ts.
//
// Guarantees:
//   * 401 when the bearer cookie is missing.
//   * 400 on an invalid body.
//   * Forwards a valid revoke to /v1/admin/users/revoke and returns 200
//     {ok:true} on the upstream 204.
//   * Passes an upstream 403 through (AC-5).
//
// Neutral `test-bearer-479` token only (slice 069 P0-A9).

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "@/lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();
vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { POST, validateRevokeBody } from "./route";

function makeReq(body: unknown) {
  return { json: () => Promise.resolve(body) } as never;
}

const USER_ID = "11111111-1111-4111-8111-111111111111";
const TENANT_ID = "22222222-2222-4222-8222-222222222222";

describe("POST /api/admin/users/revoke", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await POST(makeReq({}));
    expect(res.status).toBe(401);
  });

  test("400 on invalid body (missing tenant_id)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    const res = await POST(makeReq({ user_id: USER_ID }));
    expect(res.status).toBe(400);
  });

  test("forwards a valid revoke and returns 200 on upstream 204", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const res = await POST(makeReq({ user_id: USER_ID, tenant_id: TENANT_ID }));
    expect(res.status).toBe(200);
    const body = (await res.json()) as { ok?: boolean };
    expect(body.ok).toBe(true);
    expect(String(fetchSpy.mock.calls[0][0])).toContain(
      "/v1/admin/users/revoke",
    );
  });

  test("upstream 403 passes through (AC-5)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "cross-tenant revoke requires super_admin" }),
        {
          status: 403,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );
    const res = await POST(makeReq({ user_id: USER_ID, tenant_id: TENANT_ID }));
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toContain("super_admin");
  });
});

describe("validateRevokeBody (pure)", () => {
  test("requires user_id", () => {
    expect(validateRevokeBody({ tenant_id: TENANT_ID })).toBe(
      "user_id is required",
    );
  });
  test("requires UUID user_id", () => {
    expect(validateRevokeBody({ user_id: "x", tenant_id: TENANT_ID })).toBe(
      "user_id must be a UUID",
    );
  });
  test("requires tenant_id", () => {
    expect(validateRevokeBody({ user_id: USER_ID })).toBe(
      "tenant_id is required",
    );
  });
  test("accepts a valid revoke", () => {
    expect(
      validateRevokeBody({ user_id: USER_ID, tenant_id: TENANT_ID }),
    ).toBeNull();
  });
});
