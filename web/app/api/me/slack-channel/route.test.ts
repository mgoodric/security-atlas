// Slice 584 — vitest coverage for the /api/me/slack-channel BFF route.
//
// The route's behavior (mirrors slice-445 /api/me/email-channel):
//
//   * No session cookie  -> 401 { error }
//   * GET upstream 200    -> 200 with {enabled} passed through
//   * PUT forwards the {enabled} body verbatim with the bearer
//   * PUT default-off shape: {enabled:false} round-trips

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { GET, PUT } from "./route";

describe("GET /api/me/slack-channel", () => {
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

  test("passes upstream opted-out default through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"enabled":false}', { status: 200 }),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const body = (await res.json()) as { enabled: boolean };
    expect(body.enabled).toBe(false);
  });

  test("passes upstream opted-in through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"enabled":true}', { status: 200 }),
    );
    const res = await GET();
    const body = (await res.json()) as { enabled: boolean };
    expect(body.enabled).toBe(true);
  });

  test("targets the slack-channel upstream path", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response('{"enabled":false}', { status: 200 }),
      );
    await GET();
    expect(fetchSpy.mock.calls[0][0]).toContain("/v1/me/slack-channel");
  });
});

describe("PUT /api/me/slack-channel", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when session cookie is absent", async () => {
    const req = new Request("http://localhost/api/me/slack-channel", {
      method: "PUT",
      body: JSON.stringify({ enabled: true }),
    });
    const res = await PUT(req);
    expect(res.status).toBe(401);
  });

  test("forwards {enabled} body verbatim with bearer + PUT method", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response('{"enabled":true}', { status: 200 }));
    const req = new Request("http://localhost/api/me/slack-channel", {
      method: "PUT",
      body: JSON.stringify({ enabled: true }),
    });
    const res = await PUT(req);
    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalled();
    const call = fetchSpy.mock.calls[0];
    expect(call[0]).toContain("/v1/me/slack-channel");
    expect(call[1]?.method).toBe("PUT");
    expect(call[1]?.body).toBe('{"enabled":true}');
    const headers = call[1]?.headers as Record<string, string>;
    expect(headers["Authorization"]).toBe("Bearer test-bearer");
  });
});
