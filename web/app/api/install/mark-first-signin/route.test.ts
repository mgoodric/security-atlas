// Slice 073 — vitest seed coverage for the /api/install/mark-first-signin
// BFF route (AC-13).
//
// The route's behavior:
//
//   * No session cookie  -> 401 { error: "unauthenticated" }
//   * Upstream 200       -> 200 { marked, file_deleted } (or whatever
//                            JSON the upstream returned, including {})
//   * Upstream 401       -> 401 { error: "unauthenticated" }
//   * Upstream other     -> 502 { error: "upstream <status>" }
//   * fetch throws       -> 502 { error: "upstream fetch failed: ..." }

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_VALUE } from "../../../../lib/test-utils/test-tokens";

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

describe("POST /api/install/mark-first-signin", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when session cookie is missing", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await POST();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("unauthenticated");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards 200 with body from upstream", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_VALUE);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ marked: true, file_deleted: true }), {
        status: 200,
      }),
    );
    const res = await POST();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      marked: boolean;
      file_deleted: boolean;
    };
    expect(body.marked).toBe(true);
    expect(body.file_deleted).toBe(true);
  });

  test("idempotent re-call passes marked=false through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_VALUE);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ marked: false, file_deleted: false }), {
        status: 200,
      }),
    );
    const res = await POST();
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      marked: boolean;
      file_deleted: boolean;
    };
    expect(body.marked).toBe(false);
    expect(body.file_deleted).toBe(false);
  });

  test("translates upstream 401 to 401", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-expired-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("", { status: 401 }),
    );
    const res = await POST();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("unauthenticated");
  });

  test("translates upstream 503 to 502 with error message", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_VALUE);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("", { status: 503 }),
    );
    const res = await POST();
    expect(res.status).toBe(502);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("upstream 503");
  });

  test("translates upstream 500 to 502 with error message", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_VALUE);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("", { status: 500 }),
    );
    const res = await POST();
    expect(res.status).toBe(502);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe("upstream 500");
  });

  test("translates fetch throw to 502", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_VALUE);
    vi.spyOn(globalThis, "fetch").mockRejectedValueOnce(
      new Error("network down"),
    );
    const res = await POST();
    expect(res.status).toBe(502);
    const body = (await res.json()) as { error: string };
    expect(body.error).toContain("upstream fetch failed");
    expect(body.error).toContain("network down");
  });
});
