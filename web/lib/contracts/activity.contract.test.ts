// Slice 409 — contract-test-tier rollout (consumer side: GET /v1/activity,
// dashboard activity-feed panel).
//
// PROVIDER: internal/api/dashboard/handler_contract_test.go records the
// real handler's bodies into activity.golden.json. This CONSUMER half
// asserts the BFF (web/app/api/dashboard/activity/route.ts) against them.
// The BFF (dashboardProxy + getActivity) is a verbatim passthrough.
//
// Load-bearing field assumptions (ActivityFeedResponse in
// web/lib/api/dashboard.ts):
//   * activity is always an array (never null) — empty set is []
//   * count is a number, next_cursor is a string ("" when no next page)
//   * each event carries string ts/event_type/actor/resource_type/
//     resource_id; summary is forwarded as-is (object OR null)

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

import { GET } from "../../app/api/dashboard/activity/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "activity.golden.json"), "utf8"),
) as Golden;

describe("contract: GET /api/dashboard/activity <-> atlas GET /v1/activity", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/activity");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        Array.isArray(body.activity),
        `${name}.activity must be an array`,
      ).toBe(true);
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(typeof body.next_cursor, `${name}.next_cursor`).toBe("string");
      for (const ev of body.activity as Record<string, unknown>[]) {
        expect(typeof ev.ts).toBe("string");
        expect(typeof ev.event_type).toBe("string");
        expect(typeof ev.actor).toBe("string");
        expect(typeof ev.resource_type).toBe("string");
        expect(typeof ev.resource_id).toBe("string");
        // summary is forwarded as-is: an object OR null (never absent).
        expect("summary" in ev, `${name} event must carry summary`).toBe(true);
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
