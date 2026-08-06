// Slice 536b-1 — vitest coverage for the crosswalk-review BFF route.
//
// The route is a thin admin proxy: it must (1) 401 without the bearer
// cookie before any upstream call, (2) forward ONLY the whitelisted query
// params to /v1/admin/crosswalk-review, (3) pass the upstream status + body
// (edges + slice-536a conflicts) through verbatim — including a non-admin
// 403 — and (4) mark the mutable review queue no-store.

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "../../../../lib/test-utils/test-tokens";

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

describe("GET /api/admin/crosswalk-review BFF", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("401 without the bearer cookie, before any upstream call", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET(
      new Request(
        "http://bff/api/admin/crosswalk-review?framework_version_id=fv1",
      ),
    );
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards whitelisted query params and passes the review payload through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const upstream = {
      framework_version_id: "0d4e77aa-0000-0000-0000-000000000001",
      total_edges: 2,
      limit: 50,
      offset: 0,
      edges: [
        {
          edge_id: "e1",
          requirement_code: "A.5.15",
          relationship_type: "equal",
          strength: 1,
          mapping_tier: "draft",
          source_attribution: "community_draft",
        },
      ],
      conflicts: [
        {
          kind: "competing_anchors",
          reason: "duplicate_equal_claim",
          severity: "high",
          requirement_code: "A.5.15",
          edge_ids: ["e1", "e2"],
        },
      ],
    };
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify(upstream), { status: 200 }),
      );

    const res = await GET(
      new Request(
        "http://bff/api/admin/crosswalk-review?framework_version_id=0d4e77aa-0000-0000-0000-000000000001&limit=50&offset=0&evil=1",
      ),
    );

    expect(res.status).toBe(200);
    const url = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(url).toContain(
      "/v1/admin/crosswalk-review?framework_version_id=0d4e77aa-0000-0000-0000-000000000001&limit=50&offset=0",
    );
    // Non-whitelisted params are dropped, not forwarded.
    expect(url).not.toContain("evil");
    // Bearer forwarded as Authorization.
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit;
    expect((init.headers as Record<string, string>).Authorization).toBe(
      `Bearer ${TEST_BEARER_TOKEN}`,
    );
    // Conflicts from the 536a module ride through verbatim.
    const body = (await res.json()) as typeof upstream;
    expect(body.conflicts[0].reason).toBe("duplicate_equal_claim");
    expect(body.edges[0].mapping_tier).toBe("draft");
    // Mutable review queue is never browser-cached.
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  test("passes a non-admin 403 through verbatim", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "admin credential required" }), {
        status: 403,
      }),
    );
    const res = await GET(
      new Request(
        "http://bff/api/admin/crosswalk-review?framework_version_id=fv1",
      ),
    );
    expect(res.status).toBe(403);
  });
});
