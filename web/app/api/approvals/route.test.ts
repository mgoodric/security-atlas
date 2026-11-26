// #2119 — /api/approvals BFF: 401 without a session, otherwise the
// upstream /v1/approvals response passes through with the bearer
// forwarded.

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

describe("GET /api/approvals", () => {
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
          '{"items":[],"counts":{"exception":0,"risk_acceptance":0,"policy":0,"framework_scope":0},"total":0,"truncated":false}',
          { status: 200 },
        ),
      );
    const res = await GET();
    expect(res.status).toBe(200);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/approvals");
    expect((init?.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer",
    );
    const body = (await res.json()) as { total: number };
    expect(body.total).toBe(0);
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
