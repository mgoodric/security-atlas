// Slice 536b — vitest coverage for the crosswalk edge audit-trail BFF route.
//
// This surface is the in-product proof that no content edit and no tier
// transition went unrecorded, so the two things worth pinning are that it
// reaches the right upstream path and that it is never browser-cached: a stale
// copy would show a reviewer their own edit as absent from the trail, which is
// exactly the reading the trail exists to refute.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "@/lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "@/lib/test-utils/test-tokens";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { GET } from "./route";

const EDGE_ID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";

function call(id = EDGE_ID): Promise<Response> {
  return GET(new Request(`http://bff/api/admin/crosswalk-edges/${id}/audit`), {
    params: Promise.resolve({ id }),
  });
}

describe("GET /api/admin/crosswalk-edges/[id]/audit", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
  });

  test("forwards to the upstream audit path and returns both trails", async () => {
    let capturedURL = "";
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (input: RequestInfo | URL) => {
        capturedURL = typeof input === "string" ? input : input.toString();
        return new Response(
          JSON.stringify({
            edge_id: EDGE_ID,
            content_edits: [{ id: "c1" }],
            tier_transitions: [{ from_tier: "draft", to_tier: "under_review" }],
            current_tier: "under_review",
            content_edit_count: 1,
          }),
          { status: 200 },
        );
      },
    );

    const res = await call();

    expect(res.status).toBe(200);
    expect(capturedURL).toBe(
      `http://atlas:8080/v1/admin/crosswalk-edges/${EDGE_ID}/audit`,
    );
    const body = (await res.json()) as {
      content_edits: unknown[];
      tier_transitions: unknown[];
      content_edit_count: number;
    };
    expect(body.content_edits).toHaveLength(1);
    expect(body.tier_transitions).toHaveLength(1);
    expect(body.content_edit_count).toBe(1);
  });

  test("sets Cache-Control: no-store", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("{}", { status: 200 }),
    );
    const res = await call();
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  test("401s without a bearer cookie and never calls upstream", async () => {
    cookieStore.clear();
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200 }));

    const res = await call();

    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test.each([["not-a-uuid"], [".."], ["%2e%2e%2fadmin"], [""]])(
    "400s on the malformed edge id %j and never calls upstream",
    async (id) => {
      const fetchSpy = vi
        .spyOn(globalThis, "fetch")
        .mockResolvedValue(new Response("{}", { status: 200 }));

      const res = await call(id);

      expect(res.status).toBe(400);
      expect(fetchSpy).not.toHaveBeenCalled();
    },
  );

  test("passes an upstream 404 through verbatim", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unknown crosswalk edge id" }), {
        status: 404,
      }),
    );

    const res = await call();

    expect(res.status).toBe(404);
    expect((await res.json()) as { error: string }).toEqual({
      error: "unknown crosswalk edge id",
    });
  });
});
