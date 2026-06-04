// Slice 411 — contract-test-tier rollout (consumer side: GET
// /v1/controls/{id}/policies, control-detail Policies card).
//
// PROVIDER: internal/api/controldetail/handler_contract_test.go records the
// real Policies handler's bodies into control-policies.golden.json. This
// CONSUMER half asserts the BFF (web/app/api/controls/[id]/policies/route.ts)
// against them. The BFF is a VERBATIM passthrough: getControlPolicies
// (web/lib/api/control-detail.ts) returns res.json() unchanged and the route
// does NextResponse.json(body) — so the assert is toEqual(golden), NOT
// transform-aware like slice 410's risks BFF.
//
// Load-bearing field assumptions (ControlLinkedPoliciesResponse in
// web/lib/api/control-detail.ts):
//   * policies is always an array (never null) — empty set is []
//   * control_id / count present; each policy carries string
//     policy_id/title/version/status

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

import { GET } from "../../app/api/controls/[id]/policies/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "control-policies.golden.json"), "utf8"),
) as Golden;

const CONTROL_ID = "11111111-1111-4111-8111-111111111111";
const ctx = { params: Promise.resolve({ id: CONTROL_ID }) };

describe("contract: GET /api/controls/[id]/policies <-> atlas GET /v1/controls/{id}/policies", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/{id}/policies");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.control_id, `${name}.control_id`).toBe("string");
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(
        Array.isArray(body.policies),
        `${name}.policies must be an array`,
      ).toBe(true);
      for (const p of body.policies as Record<string, unknown>[]) {
        expect(typeof p.policy_id, `${name}.policy_id`).toBe("string");
        expect(typeof p.title, `${name}.title`).toBe("string");
        expect(typeof p.version, `${name}.version`).toBe("string");
        expect(typeof p.status, `${name}.status`).toBe("string");
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
