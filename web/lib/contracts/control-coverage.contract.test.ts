// Slice 687 — contract-test-tier rollout (consumer side: GET
// /v1/controls/{id}/coverage, the control-detail coverage tab).
//
// PROVIDER: internal/api/ucfcoverage/handler_contract_test.go records the real
// ControlCoverage handler's bodies into control-coverage.golden.json. This
// CONSUMER half asserts the BFF (web/app/api/controls/[id]/coverage/route.ts)
// against them. The BFF is a VERBATIM passthrough: getControlCoverage
// (web/lib/api/control-detail.ts) returns res.json() unchanged and the route
// does NextResponse.json(coverage) — so the assert is toEqual(golden), NOT
// transform-aware like slice 410's risks BFF.
//
// This pins the load-bearing control-detail tail read the /e2e/ suite still
// hand-mocks after slice 412 (web/e2e/control-detail-tabs.spec.ts route-fulfills
// the /coverage BFF). It is the route slice 412 D5 deferred on a seam-cost
// judgment; slice 687 lands it via a thin read-model seam.
//
// Load-bearing field assumptions (ControlCoverage + CoverageRequirement in
// web/lib/api/control-detail.ts):
//   * control is always present (object)
//   * anchor is object | null — null on the unanchored variant (control exists
//     but isn't mapped to the SCF graph); the consumer renders "not yet mapped"
//   * requirements is always an array (never null) — empty on the unanchored
//     and empty-pin variants
//   * each requirement carries number strength + string framework_version_id
//   * requirements[].coverage is number | null — null = out-of-scope/no-data;
//     the frontend MUST NOT confuse null with 0 (slice 256 P0-1)

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

import { GET } from "../../app/api/controls/[id]/coverage/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "control-coverage.golden.json"), "utf8"),
) as Golden;

const CONTROL_ID = "11111111-1111-4111-8111-111111111111";
const ctx = { params: Promise.resolve({ id: CONTROL_ID }) };

describe("contract: GET /api/controls/[id]/coverage <-> atlas GET /v1/controls/{id}/coverage", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/{id}/coverage");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.control, `${name}.control`).toBe("object");
      // anchor present-as-object OR null, never undefined/absent.
      expect("anchor" in body, `${name}.anchor present`).toBe(true);
      expect(["object"], `${name}.anchor`).toContain(typeof body.anchor);
      expect(
        Array.isArray(body.requirements),
        `${name}.requirements must be an array`,
      ).toBe(true);
      for (const req of body.requirements as Record<string, unknown>[]) {
        expect(typeof req.strength, `${name}.strength`).toBe("number");
        expect(
          typeof req.framework_version_id,
          `${name}.framework_version_id`,
        ).toBe("string");
        // coverage is number | null — present, never undefined; null must NOT
        // be confused with 0 (slice 256 P0-1).
        expect("coverage" in req, `${name}.coverage present`).toBe(true);
        expect(["number", "object"], `${name}.coverage`).toContain(
          typeof req.coverage,
        );
      }
    }
  });

  test("the unanchored variant records anchor:null + empty requirements (not 404)", () => {
    const unanchored = golden.variants.unanchored;
    expect(unanchored, "unanchored variant present").toBeDefined();
    expect(unanchored.anchor).toBeNull();
    expect(unanchored.requirements).toEqual([]);
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
