// Slice 689 — contract-test-tier rollout (consumer side: GET /v1/samples/{id},
// the audit-workspace single-sample read).
//
// PROVIDER: internal/api/audit/contractrecord_test.go records the real
// GetSample handler's bodies into sample-get.golden.json. This CONSUMER half
// asserts the BFF (web/app/api/audit/samples/[id]/route.ts) against them. The
// BFF is a VERBATIM passthrough: forwardJSON forwards the upstream body text
// unchanged — so the assert is toEqual(golden).
//
// Load-bearing field assumptions:
//   * sample is an object carrying string id/population_id/seed/created_by
//   * n is a number
//   * evidence_record_ids is ALWAYS an array (never null); empty sample -> []

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

import { GET } from "../../app/api/audit/samples/[id]/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "sample-get.golden.json"), "utf8"),
) as Golden;

const SAMPLE_ID = "33333333-3333-4333-8333-333333333333";
const ctx = { params: Promise.resolve({ id: SAMPLE_ID }) };

describe("contract: GET /api/audit/samples/[id] <-> atlas GET /v1/samples/{id}", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/samples/{id}");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      const s = body.sample as Record<string, unknown>;
      expect(typeof s, `${name}.sample`).toBe("object");
      expect(typeof s.id, `${name}.sample.id`).toBe("string");
      expect(typeof s.population_id, `${name}.sample.population_id`).toBe(
        "string",
      );
      expect(typeof s.n, `${name}.sample.n`).toBe("number");
      expect(
        Array.isArray(s.evidence_record_ids),
        `${name}.evidence_record_ids must be an array`,
      ).toBe(true);
      for (const id of s.evidence_record_ids as unknown[]) {
        expect(typeof id, `${name}.evidence_record_ids[]`).toBe("string");
      }
    }
  });

  test("empty sample records evidence_record_ids:[] (never null)", () => {
    const empty = golden.variants.empty.sample as Record<string, unknown>;
    expect(empty.evidence_record_ids).toEqual([]);
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
