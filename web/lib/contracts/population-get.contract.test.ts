// Slice 689 — contract-test-tier rollout (consumer side: GET
// /v1/populations/{id}, the audit-workspace single-population read).
//
// PROVIDER: internal/api/audit/contractrecord_test.go records the real
// GetPopulation handler's bodies into population-get.golden.json. This
// CONSUMER half asserts the BFF (web/app/api/audit/populations/[id]/route.ts)
// against them. The BFF is a VERBATIM passthrough: forwardJSON forwards the
// upstream body text unchanged — so the assert is toEqual(golden).
//
// Load-bearing field assumptions:
//   * population is an object carrying string id/control_id/created_by
//   * row_count is a number; scope_predicate is opaque JSON (defaults to
//     {"op":"true"} when the population carries no predicate)
//   * frozen_at is present ONLY on a frozen population (invariant 10)

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

import { GET } from "../../app/api/audit/populations/[id]/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "population-get.golden.json"), "utf8"),
) as Golden;

const POPULATION_ID = "22222222-2222-4222-8222-222222222222";
const ctx = { params: Promise.resolve({ id: POPULATION_ID }) };

describe("contract: GET /api/audit/populations/[id] <-> atlas GET /v1/populations/{id}", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/populations/{id}");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      const pop = body.population as Record<string, unknown>;
      expect(typeof pop, `${name}.population`).toBe("object");
      expect(typeof pop.id, `${name}.population.id`).toBe("string");
      expect(typeof pop.control_id, `${name}.population.control_id`).toBe(
        "string",
      );
      expect(typeof pop.row_count, `${name}.population.row_count`).toBe(
        "number",
      );
      expect(pop.scope_predicate, `${name}.scope_predicate`).toBeDefined();
    }
  });

  test("frozen variant carries frozen_at; open variant omits it (invariant 10)", () => {
    const frozen = golden.variants.frozen.population as Record<string, unknown>;
    const open = golden.variants.open.population as Record<string, unknown>;
    expect(typeof frozen.frozen_at).toBe("string");
    expect(open.frozen_at).toBeUndefined();
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

      const res = await GET({} as never, ctx);
      expect(res.status).toBe(200);
      const got = (await res.json()) as Record<string, unknown>;
      expect(got).toEqual(providerBody);
    });
  }

  test("returns 401 when the session cookie is absent (guard before upstream)", async () => {
    const res = await GET({} as never, ctx);
    expect(res.status).toBe(401);
  });
});
