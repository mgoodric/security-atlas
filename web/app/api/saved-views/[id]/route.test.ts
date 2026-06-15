// Slice 746 — vitest coverage for the saved-views DELETE BFF route.
//
// The DELETE response is a mutating response for per-user mutable state and
// must never be browser-cached (companion to the GET no-store fix). This
// test asserts the header is present and the upstream status passes
// through, and that the path id is forwarded encoded to the upstream
// (P0-448-5: the upstream scopes the delete to the caller's user_id).

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
import { DELETE } from "./route";

describe("DELETE /api/saved-views/[id] — no-store cache header (slice 746)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("sets Cache-Control: no-store and forwards an encoded DELETE", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    let capturedURL = "";
    let capturedMethod: string | undefined;
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        capturedURL = typeof input === "string" ? input : input.toString();
        capturedMethod = init?.method;
        // A real 204 upstream carries a null body (undici rejects a 204
        // with any body in the Response constructor).
        return new Response(null, { status: 204 });
      },
    );

    const res = await DELETE(new Request("http://bff/api/saved-views/v1"), {
      params: Promise.resolve({ id: "v1" }),
    });

    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(res.status).toBe(204);
    expect(capturedMethod).toBe("DELETE");
    expect(capturedURL).toBe("http://atlas:8080/v1/saved-views/v1");
  });
});
