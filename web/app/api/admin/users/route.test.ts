// Slice 479 — vitest coverage for web/app/api/admin/users/route.ts.
//
// The route guarantees:
//   * 401 when the bearer cookie is missing (GET + POST).
//   * GET forwards the bearer + returns {items, cross_tenant, next_cursor};
//     cross_tenant derives true for the super_admin shape (every row tagged
//     with tenant_id) and false for the within-tenant shape.
//   * GET passes an upstream 403 (tenant-admin reaching cross-tenant) through
//     as a 403 with the upstream message (AC-5 authz-honesty).
//   * POST (assign) validates the body (tenant_id/roles/user_id-vs-self),
//     forwards to /v1/admin/users/assign, and passes upstream errors through.
//
// All test bearers use the neutral `test-bearer-479` token — NO vendor
// token prefixes (`ghp_*`, `sk_*`, `eyJ*`, `AKIA*`) per slice 069 P0-A9.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "@/lib/test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();
vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET, POST, validateAssignBody } from "./route";

// makeGetReq builds the minimal NextRequest shape the GET handler reads.
function makeGetReq(url = "http://test/api/admin/users") {
  const u = new URL(url);
  return { nextUrl: { searchParams: u.searchParams } } as never;
}

// makePostReq builds the minimal NextRequest shape the POST handler reads.
function makePostReq(body: unknown) {
  return {
    json: () => Promise.resolve(body),
  } as never;
}

const SUPER_ROW = {
  id: "11111111-1111-4111-8111-111111111111",
  tenant_id: "22222222-2222-4222-8222-222222222222",
  email: "a@example.com",
  display_name: "Alpha",
  status: "active",
  roles: ["admin"],
};
const WITHIN_ROW = {
  id: "33333333-3333-4333-8333-333333333333",
  email: "b@example.com",
  display_name: "Bravo",
  status: "active",
  roles: ["viewer"],
};

describe("GET /api/admin/users", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(makeGetReq());
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("super_admin shape → cross_tenant=true", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ items: [SUPER_ROW] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await GET(makeGetReq());
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      items: unknown[];
      cross_tenant: boolean;
    };
    expect(body.cross_tenant).toBe(true);
    expect(body.items).toHaveLength(1);
    // Forwarded the bearer.
    const call = fetchSpy.mock.calls[0];
    expect(String(call[0])).toContain("/v1/admin/users");
    expect(
      (call[1] as RequestInit).headers as Record<string, string>,
    ).toMatchObject({ Authorization: "Bearer test-bearer-479" });
  });

  test("within-tenant shape → cross_tenant=false", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ items: [WITHIN_ROW] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const res = await GET(makeGetReq());
    const body = (await res.json()) as { cross_tenant: boolean };
    expect(body.cross_tenant).toBe(false);
  });

  test("upstream 403 passes through with message (AC-5)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("forbidden", { status: 403 }),
    );
    const res = await GET(makeGetReq());
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toBeTruthy();
  });

  test("forwards cursor + limit query params upstream", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ items: [], next_cursor: "" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await GET(makeGetReq("http://test/api/admin/users?cursor=abc&limit=25"));
    expect(String(fetchSpy.mock.calls[0][0])).toContain("cursor=abc");
    expect(String(fetchSpy.mock.calls[0][0])).toContain("limit=25");
  });
});

describe("POST /api/admin/users (assign)", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => vi.restoreAllMocks());

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await POST(makePostReq({}));
    expect(res.status).toBe(401);
  });

  test("400 on invalid body (missing roles)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    const res = await POST(
      makePostReq({ user_id: SUPER_ROW.id, tenant_id: SUPER_ROW.tenant_id }),
    );
    expect(res.status).toBe(400);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toContain("role");
  });

  test("forwards a valid assign + returns the upstream body", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          user_id: SUPER_ROW.id,
          tenant_id: SUPER_ROW.tenant_id,
          roles: ["viewer"],
          idp_issuer: "urn:atlas:local",
          idp_subject: SUPER_ROW.id,
          membership_created: true,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await POST(
      makePostReq({
        user_id: SUPER_ROW.id,
        tenant_id: SUPER_ROW.tenant_id,
        roles: ["viewer"],
      }),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as { membership_created?: boolean };
    expect(body.membership_created).toBe(true);
    expect(String(fetchSpy.mock.calls[0][0])).toContain(
      "/v1/admin/users/assign",
    );
  });

  test("self_assign omits user_id and forwards self_assign=true", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          user_id: SUPER_ROW.id,
          tenant_id: SUPER_ROW.tenant_id,
          roles: ["admin"],
          idp_issuer: "urn:atlas:local",
          idp_subject: SUPER_ROW.id,
          membership_created: true,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await POST(
      makePostReq({
        tenant_id: SUPER_ROW.tenant_id,
        roles: ["admin"],
        self_assign: true,
      }),
    );
    expect(res.status).toBe(200);
    const sentBody = JSON.parse(
      (fetchSpy.mock.calls[0][1] as RequestInit).body as string,
    ) as { self_assign?: boolean; user_id?: string };
    expect(sentBody.self_assign).toBe(true);
    expect(sentBody.user_id).toBeUndefined();
  });

  test("upstream 403 passes through (AC-5)", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-479" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "cross-tenant assignment requires super_admin",
        }),
        { status: 403, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await POST(
      makePostReq({
        user_id: SUPER_ROW.id,
        tenant_id: SUPER_ROW.tenant_id,
        roles: ["viewer"],
      }),
    );
    expect(res.status).toBe(403);
    const body = (await res.json()) as { error?: string };
    expect(body.error).toContain("super_admin");
  });
});

describe("validateAssignBody (pure)", () => {
  test("requires tenant_id", () => {
    expect(
      validateAssignBody({ roles: ["viewer"], user_id: SUPER_ROW.id }),
    ).toBe("tenant_id is required");
  });
  test("requires UUID tenant_id", () => {
    expect(
      validateAssignBody({
        tenant_id: "not-a-uuid",
        roles: ["viewer"],
        user_id: SUPER_ROW.id,
      }),
    ).toBe("tenant_id must be a UUID");
  });
  test("requires at least one role", () => {
    expect(
      validateAssignBody({
        tenant_id: SUPER_ROW.tenant_id,
        roles: [],
        user_id: SUPER_ROW.id,
      }),
    ).toContain("role");
  });
  test("requires user_id unless self_assign", () => {
    expect(
      validateAssignBody({ tenant_id: SUPER_ROW.tenant_id, roles: ["viewer"] }),
    ).toContain("user_id is required");
  });
  test("accepts self_assign without user_id", () => {
    expect(
      validateAssignBody({
        tenant_id: SUPER_ROW.tenant_id,
        roles: ["viewer"],
        self_assign: true,
      }),
    ).toBeNull();
  });
  test("accepts a valid explicit assign", () => {
    expect(
      validateAssignBody({
        tenant_id: SUPER_ROW.tenant_id,
        roles: ["viewer"],
        user_id: SUPER_ROW.id,
      }),
    ).toBeNull();
  });
});
