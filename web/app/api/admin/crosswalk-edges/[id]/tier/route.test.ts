// Slice 536b-1 — vitest coverage for the crosswalk-tier BFF route (the
// approve/reject verb of the review surface).
//
// The route is a thin forward to slice 483's tier state machine — the ONE
// review lifecycle (536a decisions-log §1.2). It must (1) 401 without the
// bearer cookie before any upstream call, (2) forward the transition body
// verbatim to POST /v1/admin/crosswalk-edges/{id}/tier, and (3) pass the
// upstream verdict through verbatim — including the 422 illegal-transition
// refusal (e.g. the draft -> verified skip: nothing auto-approves, and the
// BFF cannot smuggle a transition past 483's legality check) and a
// non-admin 403.

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "../../../../../../lib/test-utils/test-tokens";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { POST } from "./route";

function makeReq(body: unknown): { json: () => Promise<unknown> } {
  return { json: async () => body };
}

function paramsFor(id: string): { params: Promise<{ id: string }> } {
  return { params: Promise.resolve({ id }) };
}

describe("POST /api/admin/crosswalk-edges/[id]/tier BFF", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("401 without the bearer cookie, before any upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await POST(
      makeReq({ tier: "verified" }) as never,
      paramsFor("e1"),
    );
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards the approve transition to slice 483's tier endpoint verbatim", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const upstream = {
      edge_id: "0d4e77aa-0000-0000-0000-00000000000e",
      from_tier: "under_review",
      to_tier: "verified",
      note: "checked against ISO source text",
    };
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(upstream), { status: 200 }),
      );

    const res = await POST(
      makeReq({
        tier: "verified",
        note: "checked against ISO source text",
      }) as never,
      paramsFor("0d4e77aa-0000-0000-0000-00000000000e"),
    );

    expect(res.status).toBe(200);
    const url = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(url).toContain(
      "/v1/admin/crosswalk-edges/0d4e77aa-0000-0000-0000-00000000000e/tier",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>).Authorization).toBe(
      `Bearer ${TEST_BEARER_TOKEN}`,
    );
    const sent = String(init.body ?? "");
    expect(sent).toContain('"tier":"verified"');
    const body = (await res.json()) as typeof upstream;
    expect(body.to_tier).toBe("verified");
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  test("passes the 422 illegal-transition refusal through verbatim", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: "illegal tier transition draft -> verified" }),
        { status: 422 },
      ),
    );
    // The draft -> verified skip: 483's state machine refuses it, and the
    // BFF has no path around that verdict.
    const res = await POST(
      makeReq({ tier: "verified" }) as never,
      paramsFor("e1"),
    );
    expect(res.status).toBe(422);
  });

  test("passes a non-admin 403 through verbatim", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "admin credential required" }), {
        status: 403,
      }),
    );
    const res = await POST(
      makeReq({ tier: "rejected" }) as never,
      paramsFor("e1"),
    );
    expect(res.status).toBe(403);
  });
});
