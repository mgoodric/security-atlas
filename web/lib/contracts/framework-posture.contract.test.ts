// Slice 409 — contract-test-tier rollout (consumer side: GET
// /v1/frameworks/posture, dashboard framework-posture panel).
//
// The PROVIDER half (internal/api/dashboard/handler_contract_test.go)
// records the real Go handler's GET /v1/frameworks/posture bodies into
// framework-posture.golden.json. This CONSUMER half asserts the Next.js
// BFF (web/app/api/dashboard/framework-posture/route.ts) against those
// recorded bodies — closing the silent mock-vs-reality gap (ADR-0007) on
// a high-traffic dashboard panel the /e2e/ suite traverses (slice 394's
// unblocking precondition).
//
// The framework-posture BFF (dashboardProxy + getFrameworkPosture) is a
// verbatim passthrough of the upstream envelope, so the consumer
// assertion is total: the BFF must emit exactly the recorded body.
//
// Load-bearing field assumptions (FrameworkPostureReport in
// web/lib/api/dashboard.ts):
//   * frameworks is always an array (never null) — empty set is []
//   * count is a number
//   * each row carries string framework_id/framework_version + numeric
//     coverage_pct/freshness_composite/trend_delta_90d

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

import { GET } from "../../app/api/dashboard/framework-posture/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "framework-posture.golden.json"), "utf8"),
) as Golden;

describe("contract: GET /api/dashboard/framework-posture <-> atlas GET /v1/frameworks/posture", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/frameworks/posture");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        Array.isArray(body.frameworks),
        `${name}.frameworks must be an array`,
      ).toBe(true);
      expect(typeof body.count, `${name}.count`).toBe("number");
      for (const row of body.frameworks as Record<string, unknown>[]) {
        expect(typeof row.framework_id).toBe("string");
        expect(typeof row.framework_version).toBe("string");
        expect(typeof row.coverage_pct).toBe("number");
        expect(typeof row.freshness_composite).toBe("number");
        expect(typeof row.trend_delta_90d).toBe("number");
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
