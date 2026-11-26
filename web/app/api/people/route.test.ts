// #2085 — /api/people BFF: 401 without a session, otherwise the upstream
// /v1/people response passes through with the bearer forwarded.

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

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { GET } from "./route";

describe("GET /api/people", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("returns 401 when session cookie is absent", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await GET();
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards the bearer and passes the upstream body through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(
          '{"users":[{"id":"u1","display_name":"Alice","status":"active"}],"api_keys":[]}',
          { status: 200 },
        ),
      );
    const res = await GET();
    expect(res.status).toBe(200);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/people");
    expect((init?.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer",
    );
    const body = (await res.json()) as { users: { display_name: string }[] };
    expect(body.users[0].display_name).toBe("Alice");
  });

  // #2174: an assignee picker asks for the members of one module.
  test("forwards ?module= and nothing else", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response('{"users":[],"api_keys":[]}', { status: 200 }),
      );
    await GET(
      new Request("http://web/api/people?module=third_party_risk&other=x"),
    );
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      "http://atlas:8080/v1/people?module=third_party_risk",
    );
  });

  test("passes an upstream 403 through", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response('{"error":"forbidden"}', { status: 403 }),
    );
    const res = await GET();
    expect(res.status).toBe(403);
  });
});
