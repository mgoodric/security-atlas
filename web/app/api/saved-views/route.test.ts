// Slice 746 — vitest coverage for the saved-views BFF route's no-store header.
//
// Saved views are per-(tenant, user) mutable state. After a DELETE, the
// React-Query refetch of GET /v1/saved-views must NOT be answered from the
// browser HTTP cache (a stale, pre-delete body leaves the deleted view in
// the controls `<select>` until a hard reload — slice 743 finding / slice
// 448 AC-5). The fix attaches `Cache-Control: no-store` to the saved-views
// BFF responses, PER ROUTE, via the opt-in `noStore` wrapper — the shared
// `forwardJSON` default is left unchanged (its own default-path test lives
// in lib/api/bff.test.ts so cache-friendly GET routes provably do not
// regress).
//
// These tests assert the header is present on the route handler's response
// (the browser-facing surface), and that the upstream body + status still
// pass through verbatim.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../lib/test-utils/next-mocks";
import { TEST_BEARER_TOKEN } from "../../../lib/test-utils/test-tokens";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { GET, POST } from "./route";

describe("saved-views BFF route — no-store cache header (slice 746)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("GET sets Cache-Control: no-store on the browser-facing response", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ views: [{ id: "v1", name: "Weekly" }] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET();

    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(res.headers.get("Pragma")).toBe("no-cache");
    // Body + status still pass through verbatim.
    expect(res.status).toBe(200);
    const body = (await res.json()) as { views: { id: string }[] };
    expect(body.views).toHaveLength(1);
    expect(body.views[0].id).toBe("v1");
    // The existing Content-Type header is preserved (not clobbered).
    expect(res.headers.get("Content-Type")).toContain("application/json");
  });

  test("GET without a session cookie still no-stores the 401", async () => {
    // forwardJSON short-circuits to 401 before any upstream call; the
    // wrapper must not depend on the upstream having been hit.
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("POST sets Cache-Control: no-store and forwards the created view", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    let capturedInit: RequestInit | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        capturedInit = init;
        return new Response(JSON.stringify({ id: "v2", name: "New" }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      },
    );

    const req = new Request("http://bff/api/saved-views", {
      method: "POST",
      body: JSON.stringify({ name: "New", filters: {} }),
    });
    const res = await POST(req as never);

    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(res.status).toBe(201);
    expect(capturedInit?.method).toBe("POST");
    const body = (await res.json()) as { id: string };
    expect(body.id).toBe("v2");
  });
});
