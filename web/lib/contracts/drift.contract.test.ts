// Slice 409 — contract-test-tier rollout (consumer side: GET
// /v1/controls/drift, dashboard drift panel).
//
// PROVIDER: internal/api/freshnessdrift/handler_contract_test.go records
// the real handler's bodies into drift.golden.json. This CONSUMER half
// asserts the BFF (web/app/api/dashboard/drift/route.ts) against them.
// The BFF (dashboardProxy + getControlDrift(bearer, "7d")) is a verbatim
// passthrough of the upstream envelope.
//
// Load-bearing field assumptions (DriftReport in
// web/lib/api/dashboard.ts):
//   * since / through are strings (YYYY-MM-DD)
//   * delta / flipped_out_count are numbers
//   * flipped_out is always an array (never null) — empty set is []
//   * each row carries string control_id/last_passing/current_result

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

import { GET } from "../../app/api/dashboard/drift/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "drift.golden.json"), "utf8"),
) as Golden;

describe("contract: GET /api/dashboard/drift <-> atlas GET /v1/controls/drift", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/drift");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.since, `${name}.since`).toBe("string");
      expect(typeof body.through, `${name}.through`).toBe("string");
      expect(typeof body.delta, `${name}.delta`).toBe("number");
      expect(typeof body.flipped_out_count, `${name}.flipped_out_count`).toBe(
        "number",
      );
      expect(
        Array.isArray(body.flipped_out),
        `${name}.flipped_out must be array`,
      ).toBe(true);
      for (const row of body.flipped_out as Record<string, unknown>[]) {
        expect(typeof row.control_id).toBe("string");
        expect(typeof row.last_passing).toBe("string");
        expect(typeof row.current_result).toBe("string");
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
