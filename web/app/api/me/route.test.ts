// Slice 108 — vitest seed coverage for the /api/me BFF route (proxy shape).
//
// The route's behavior:
//
//   * No session cookie  -> 401 { error }
//   * Upstream 200       -> 200 with upstream JSON body passed through
//   * Upstream 400       -> 400 with upstream JSON body passed through

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { SESSION_COOKIE } from "@/lib/auth";
import { GET, PATCH } from "./route";

describe("GET /api/me", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when session cookie is absent", async () => {
    const res = await GET();
    expect(res.status).toBe(401);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBeDefined();
  });

  test("passes upstream 200 body through", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"user_id":"u1","display_name":"Alice"}', { status: 200 }),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { user_id: string };
    expect(body.user_id).toBe("u1");
  });

  test("passes upstream 401 through", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"invalid bearer"}', { status: 401 }),
    );
    const res = await GET();
    expect(res.status).toBe(401);
  });
});

describe("PATCH /api/me", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when session cookie is absent", async () => {
    const req = new Request("http://localhost/api/me", {
      method: "PATCH",
      body: JSON.stringify({ display_name: "X" }),
    });
    const res = await PATCH(req);
    expect(res.status).toBe(401);
  });

  test("forwards body verbatim and passes upstream 400 through", async () => {
    cookieStore.set(SESSION_COOKIE, "test-bearer");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"time_zone must be a valid IANA timezone"}', {
        status: 400,
      }),
    );
    const req = new Request("http://localhost/api/me", {
      method: "PATCH",
      body: JSON.stringify({ time_zone: "Bad/Zone" }),
    });
    const res = await PATCH(req);
    expect(res.status).toBe(400);
    expect(fetchSpy).toHaveBeenCalled();
    const call = fetchSpy.mock.calls[0];
    expect(call[1]?.method).toBe("PATCH");
    expect(call[1]?.body).toBe('{"time_zone":"Bad/Zone"}');
  });
});
