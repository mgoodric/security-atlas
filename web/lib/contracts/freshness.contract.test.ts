// Slice 409 — contract-test-tier rollout (consumer side: GET
// /v1/evidence/freshness, dashboard freshness panel).
//
// PROVIDER: internal/api/freshnessdrift/handler_contract_test.go records
// the real handler's bodies into freshness.golden.json. This CONSUMER
// half asserts the BFF (web/app/api/dashboard/freshness/route.ts) against
// them. The BFF (dashboardProxy + getEvidenceFreshness) is a verbatim
// passthrough.
//
// Load-bearing field assumptions (FreshnessReport in
// web/lib/api/dashboard.ts):
//   * bucket is a string, buckets is always an array (never null)
//   * total / total_stale are numbers
//   * each bucket carries string freshness_class + numeric
//     total/fresh/stale

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

import { GET } from "../../app/api/dashboard/freshness/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "freshness.golden.json"), "utf8"),
) as Golden;

describe("contract: GET /api/dashboard/freshness <-> atlas GET /v1/evidence/freshness", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/evidence/freshness");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.bucket, `${name}.bucket`).toBe("string");
      expect(Array.isArray(body.buckets), `${name}.buckets must be array`).toBe(
        true,
      );
      expect(typeof body.total, `${name}.total`).toBe("number");
      expect(typeof body.total_stale, `${name}.total_stale`).toBe("number");
      for (const b of body.buckets as Record<string, unknown>[]) {
        expect(typeof b.freshness_class).toBe("string");
        expect(typeof b.total).toBe("number");
        expect(typeof b.fresh).toBe("number");
        expect(typeof b.stale).toBe("number");
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
