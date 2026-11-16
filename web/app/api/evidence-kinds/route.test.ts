// #2088 — /api/evidence-kinds BFF: 401 without a session, otherwise the
// schema registry list narrowed to the fields the kinds picker reads.

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

describe("GET /api/evidence-kinds", () => {
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

  test("forwards the bearer and returns kind, semver and owner only", async () => {
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          schemas: [
            {
              id: "s1",
              kind: "aws.s3.encryption.v1",
              semver: "1.0.0",
              owner: "platform",
              tenant_id: "t1",
              created_by: "u1",
            },
          ],
        }),
        { status: 200 },
      ),
    );
    const res = await GET();
    expect(res.status).toBe(200);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(String(url)).toBe("http://atlas:8080/v1/schemas?limit=500");
    expect((init?.headers as Record<string, string>).Authorization).toBe(
      "Bearer test-bearer",
    );
    expect(await res.json()).toEqual({
      schemas: [
        { kind: "aws.s3.encryption.v1", semver: "1.0.0", owner: "platform" },
      ],
    });
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
