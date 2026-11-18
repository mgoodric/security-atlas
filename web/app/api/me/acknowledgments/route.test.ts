// #2465 — vitest coverage for GET /api/me/acknowledgments: it forwards
// the cookie bearer to /v1/me/acknowledgments, passes the caller's
// pending list through uncached, and is 401 without a session.

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

describe("GET /api/me/acknowledgments (#2465)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("no session is 401 without an upstream call", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    expect(spy).not.toHaveBeenCalled();
  });

  test("forwards the bearer and passes the pending list through uncached", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, TEST_BEARER_TOKEN);
    const body = {
      pending: [{ policy_id: "p1", title: "Access Control" }],
      count: 1,
      window_seconds: 31536000,
    };
    const calls: { url: string; init?: RequestInit }[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ url: String(input), init });
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      },
    );
    const res = await GET();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual(body);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(calls[0].url).toBe("http://atlas:8080/v1/me/acknowledgments");
    expect(
      (calls[0].init?.headers as Record<string, string>).Authorization,
    ).toBe(`Bearer ${TEST_BEARER_TOKEN}`);
  });
});
