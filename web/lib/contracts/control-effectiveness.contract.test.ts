// Slice 412 — contract-test-tier rollout (consumer side: GET
// /v1/controls/{id}/effectiveness, control-detail effectiveness KPI card).
//
// PROVIDER: internal/api/controlstate/handler_contract_test.go records the
// real Effectiveness handler's bodies into control-effectiveness.golden.json.
// This CONSUMER half asserts the BFF
// (web/app/api/controls/[id]/effectiveness/route.ts) against them. The BFF is a
// VERBATIM passthrough: getControlEffectiveness (web/lib/api/control-detail.ts)
// returns res.json() unchanged and the route does
// NextResponse.json(effectiveness) — so the assert is toEqual(golden), NOT
// transform-aware like slice 410's risks BFF.
//
// Load-bearing field assumptions (ControlEffectiveness in
// web/lib/api/control-detail.ts):
//   * control_id present as a string
//   * pass_rate / pass_count / total_count are all numbers (never null) — the
//     "empty" variant pins total_count=0 + pass_rate=0 so the consumer never
//     confuses "no data yet" with "perfectly failing"
//   * window_start / window_end present as strings

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

import { GET } from "../../app/api/controls/[id]/effectiveness/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "control-effectiveness.golden.json"), "utf8"),
) as Golden;

const CONTROL_ID = "11111111-1111-4111-8111-111111111111";
const ctx = { params: Promise.resolve({ id: CONTROL_ID }) };

describe("contract: GET /api/controls/[id]/effectiveness <-> atlas GET /v1/controls/{id}/effectiveness", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/{id}/effectiveness");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.control_id, `${name}.control_id`).toBe("string");
      expect(typeof body.pass_rate, `${name}.pass_rate`).toBe("number");
      expect(typeof body.pass_count, `${name}.pass_count`).toBe("number");
      expect(typeof body.total_count, `${name}.total_count`).toBe("number");
      expect(typeof body.window_start, `${name}.window_start`).toBe("string");
      expect(typeof body.window_end, `${name}.window_end`).toBe("string");
    }
  });

  test("the empty variant pins no-data as total_count 0 (not a failing score)", () => {
    const empty = golden.variants.empty;
    expect(empty, "an 'empty' variant must exist").toBeDefined();
    expect(empty.total_count).toBe(0);
    expect(empty.pass_rate).toBe(0);
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
