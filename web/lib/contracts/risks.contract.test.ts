// Slice 410 — contract-test-tier rollout (consumer side: GET /v1/risks,
// dashboard top-risks panel).
//
// PROVIDER: internal/api/risks/handler_contract_test.go records the real
// ListRisks handler's UPSTREAM bodies ({risks: riskWire[], count}) into
// risks.golden.json. This CONSUMER half asserts the BFF
// (web/app/api/dashboard/risks/route.ts) against them.
//
// TRANSFORM-AWARE (slice 410 / 409 D1): unlike the slice-409 dashboard
// panels, this BFF is NOT a verbatim passthrough. getMitigateRisks
// (web/lib/api/dashboard.ts) unwraps the upstream body.risks; the route
// re-wraps {risks, count: risks.length}. So the assert is:
//
//   BFF output  ===  { risks: golden.risks, count: golden.risks.length }
//
// NOT toEqual(golden). The re-wrapped count is recomputed from the array
// length, so it is asserted against risks.length — pinning the BFF's own
// recount, not the upstream count field (they happen to match, but the BFF
// contract is "count === risks.length").
//
// Load-bearing field assumptions (DashboardRisk in web/lib/api/dashboard.ts):
//   * risks is always an array (never null) — empty set is []
//   * inherent_score / residual_score are opaque (unknown) — never parsed
//   * linked_control_ids is always an array of strings
//   * review_due_at / accepted_until are optional (absent on the wire when nil)
//   * id/title/category/methodology/treatment are strings

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

import { GET } from "../../app/api/dashboard/risks/route";

interface Golden {
  endpoint: string;
  variants: Record<string, { risks: Record<string, unknown>[]; count: number }>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "risks.golden.json"), "utf8"),
) as Golden;

describe("contract: GET /api/dashboard/risks <-> atlas GET /v1/risks (transform-aware)", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/risks");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant carries the upstream {risks, count} envelope", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(Array.isArray(body.risks), `${name}.risks must be an array`).toBe(
        true,
      );
      expect(typeof body.count, `${name}.count`).toBe("number");
    }
  });

  test("every risk row satisfies the DashboardRisk field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      for (const rk of body.risks) {
        expect(typeof rk.id, `${name}.id`).toBe("string");
        expect(typeof rk.title, `${name}.title`).toBe("string");
        expect(typeof rk.category, `${name}.category`).toBe("string");
        expect(typeof rk.methodology, `${name}.methodology`).toBe("string");
        expect(typeof rk.treatment, `${name}.treatment`).toBe("string");
        // inherent_score / residual_score are opaque — present but unparsed.
        expect(rk.inherent_score, `${name}.inherent_score`).toBeDefined();
        expect(rk.residual_score, `${name}.residual_score`).toBeDefined();
        // linked_control_ids is always an array of strings (never null).
        expect(
          Array.isArray(rk.linked_control_ids),
          `${name}.linked_control_ids must be an array`,
        ).toBe(true);
        for (const cid of rk.linked_control_ids as unknown[]) {
          expect(typeof cid).toBe("string");
        }
        // review_due_at / accepted_until are optional; when present, typed.
        if (rk.review_due_at !== undefined) {
          expect(typeof rk.review_due_at, `${name}.review_due_at`).toBe(
            "string",
          );
        }
        if (rk.accepted_until !== undefined && rk.accepted_until !== null) {
          expect(typeof rk.accepted_until, `${name}.accepted_until`).toBe(
            "string",
          );
        }
      }
    }
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF re-wraps provider variant "${variantName}" to {risks, count: risks.length}`, async () => {
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
      const got = (await res.json()) as { risks: unknown[]; count: number };

      // TRANSFORM-AWARE: the BFF unwraps body.risks and re-wraps with a
      // recomputed count — assert against the re-wrapped shape, NOT
      // toEqual(providerBody).
      expect(got).toEqual({
        risks: providerBody.risks,
        count: providerBody.risks.length,
      });
      // The recomputed count is the array length, independent of the
      // upstream count field.
      expect(got.count).toBe(providerBody.risks.length);
    });
  }

  test("returns 401 when the session cookie is absent (guard before upstream)", async () => {
    const res = await GET();
    expect(res.status).toBe(401);
  });
});
