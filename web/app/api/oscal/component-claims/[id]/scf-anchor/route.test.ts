// Slice 620 — vitest coverage for the PATCH /api/oscal/component-claims/[id]/scf-anchor
// BFF route (map an unmapped vendor claim to a canonical SCF anchor).
//
// Coverage matrix:
//   * No bearer cookie -> 401 { error }.
//   * Invalid JSON body -> 400.
//   * Empty/whitespace scf_anchor_id -> 400.
//   * Happy path -> forwards bearer + PATCHes the upstream; returns the
//     mapping result (unmapped=false, scf_anchor_id set).
//   * Upstream 422 (unknown anchor) -> error surfaced.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { PATCH } from "./route";

const CLAIM_ID = "33333333-3333-3333-3333-333333333333";

// The BFF handler types its first arg as NextRequest but only calls
// `.json()` on it, which a plain Request satisfies. The route-test convention
// (see app/api/controls/route.test.ts) casts the fixture with `as never`.
function makeReq(body: string): never {
  return new Request(
    `http://test/api/oscal/component-claims/${CLAIM_ID}/scf-anchor`,
    {
      method: "PATCH",
      body,
      headers: { "Content-Type": "application/json" },
    },
  ) as never;
}

function ctx() {
  return { params: Promise.resolve({ id: CLAIM_ID }) };
}

describe("PATCH /api/oscal/component-claims/[id]/scf-anchor", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when bearer cookie is absent", async () => {
    const res = await PATCH(
      makeReq(JSON.stringify({ scf_anchor_id: "TST-01" })),
      ctx(),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBeDefined();
  });

  test("returns 400 on invalid JSON", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    const res = await PATCH(makeReq("not-json"), ctx());
    expect(res.status).toBe(400);
  });

  test("returns 400 when scf_anchor_id is blank", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    const res = await PATCH(
      makeReq(JSON.stringify({ scf_anchor_id: "   " })),
      ctx(),
    );
    expect(res.status).toBe(400);
  });

  test("forwards the bearer + PATCHes upstream on happy path", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          id: CLAIM_ID,
          control_id: "ac-3",
          is_vendor_claim: true,
          claim_status: "asserted",
          scf_anchor_id: "TST-01",
          unmapped: false,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await PATCH(
      makeReq(JSON.stringify({ scf_anchor_id: "TST-01" })),
      ctx(),
    );
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      scf_anchor_id: string;
      unmapped: boolean;
      is_vendor_claim: boolean;
    };
    expect(body.scf_anchor_id).toBe("TST-01");
    expect(body.unmapped).toBe(false);
    // The claim stays a claim.
    expect(body.is_vendor_claim).toBe(true);

    // The upstream call hit the PATCH scf-anchor path with the bearer + body.
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(url).toContain(`/v1/oscal/component-claims/${CLAIM_ID}/scf-anchor`);
    expect(init.method).toBe("PATCH");
    expect((init.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer-token",
    );
    expect(JSON.parse(init.body as string)).toEqual({
      scf_anchor_id: "TST-01",
    });
  });

  test("surfaces an upstream 422 (unknown anchor)", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          error: "scf_anchor_id does not resolve to a bundled SCF anchor",
        }),
        { status: 422, headers: { "Content-Type": "application/json" } },
      ),
    );
    const res = await PATCH(
      makeReq(JSON.stringify({ scf_anchor_id: "NOPE-99" })),
      ctx(),
    );
    expect(res.status).toBe(422);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("bundled SCF anchor");
  });
});
