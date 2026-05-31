// Slice 409 — contract-test-tier rollout (consumer side: GET /v1/upcoming,
// dashboard upcoming-rollup panel).
//
// PROVIDER: internal/api/dashboard/handler_contract_test.go records the
// real handler's bodies into upcoming.golden.json. This CONSUMER half
// asserts the BFF (web/app/api/dashboard/upcoming/route.ts) against them.
// The BFF (dashboardProxy + getUpcoming) is a verbatim passthrough.
//
// Load-bearing field assumptions (UpcomingResponse in
// web/lib/api/dashboard.ts):
//   * upcoming is always an array (never null) — empty set is []
//   * count is a number, next_cursor is a string ("" when no next page)
//   * each item carries string due_date/category/title/resource_type/
//     resource_id

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";

import { GET } from "../../app/api/dashboard/upcoming/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "upcoming.golden.json"), "utf8"),
) as Golden;

describe("contract: GET /api/dashboard/upcoming <-> atlas GET /v1/upcoming", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/upcoming");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        Array.isArray(body.upcoming),
        `${name}.upcoming must be an array`,
      ).toBe(true);
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(typeof body.next_cursor, `${name}.next_cursor`).toBe("string");
      for (const it of body.upcoming as Record<string, unknown>[]) {
        expect(typeof it.due_date).toBe("string");
        expect(typeof it.category).toBe("string");
        expect(typeof it.title).toBe("string");
        expect(typeof it.resource_type).toBe("string");
        expect(typeof it.resource_id).toBe("string");
      }
    }
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
      cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer");
      const providerBody = golden.variants[variantName];
      vi.spyOn(globalThis, "fetch").mockImplementation(
        async () =>
          new Response(JSON.stringify(providerBody), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      );

      const res = await GET();
      expect(res.status).toBe(200);
      const got = (await res.json()) as Record<string, unknown>;
      expect(got).toEqual(providerBody);
    });
  }

  test("returns 401 when the session cookie is absent (guard before upstream)", async () => {
    const res = await GET();
    expect(res.status).toBe(401);
  });
});
