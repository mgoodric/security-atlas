// Slice 411 — contract-test-tier rollout (consumer side: GET
// /v1/controls/{id}/risks, control-detail linked-Risks card).
//
// PROVIDER: internal/api/controldetail/handler_contract_test.go records the
// real Risks handler's bodies into control-risks.golden.json. This CONSUMER
// half asserts the BFF (web/app/api/controls/[id]/risks/route.ts) against
// them. The BFF is a VERBATIM passthrough: getControlRisks
// (web/lib/api/control-detail.ts) returns res.json() unchanged and the route
// does NextResponse.json(body) — so the assert is toEqual(golden).
//
// Load-bearing field assumptions (ControlLinkedRisksResponse in
// web/lib/api/control-detail.ts):
//   * risks is always an array (never null) — empty set is []
//   * inherent_score / residual_score are opaque (present, possibly null) —
//     never parsed client-side
//   * link_weight is number-or-null (the per-link design_score)
//   * risk_id / title are strings

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

import { GET } from "../../app/api/controls/[id]/risks/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "control-risks.golden.json"), "utf8"),
) as Golden;

const CONTROL_ID = "11111111-1111-4111-8111-111111111111";
const ctx = { params: Promise.resolve({ id: CONTROL_ID }) };

describe("contract: GET /api/controls/[id]/risks <-> atlas GET /v1/controls/{id}/risks", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/{id}/risks");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.control_id, `${name}.control_id`).toBe("string");
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(Array.isArray(body.risks), `${name}.risks must be an array`).toBe(
        true,
      );
      for (const rk of body.risks as Record<string, unknown>[]) {
        expect(typeof rk.risk_id, `${name}.risk_id`).toBe("string");
        expect(typeof rk.title, `${name}.title`).toBe("string");
        // inherent_score / residual_score are opaque — present (may be null),
        // never absent.
        expect("inherent_score" in rk, `${name}.inherent_score`).toBe(true);
        expect("residual_score" in rk, `${name}.residual_score`).toBe(true);
        // link_weight is number-or-null (never undefined).
        expect("link_weight" in rk, `${name}.link_weight`).toBe(true);
        if (rk.link_weight !== null) {
          expect(typeof rk.link_weight, `${name}.link_weight`).toBe("number");
        }
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
