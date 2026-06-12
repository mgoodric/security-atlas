// Slice 412 — contract-test-tier rollout (consumer side: GET
// /v1/controls/{id}/state, control-detail state/freshness card).
//
// PROVIDER: internal/api/controlstate/handler_contract_test.go records the
// real State handler's bodies into control-state.golden.json. This CONSUMER
// half asserts the BFF (web/app/api/controls/[id]/state/route.ts) against
// them. The BFF is a VERBATIM passthrough: getControlState
// (web/lib/api/control-detail.ts) returns res.json() unchanged and the route
// does NextResponse.json(state) — so the assert is toEqual(golden), NOT
// transform-aware like slice 410's risks BFF.
//
// Load-bearing field assumptions (ControlStateResponse + ControlStateEntry in
// web/lib/api/control-detail.ts):
//   * states is always an array (never null) — empty set is []
//   * control_id / count present; each entry carries string result /
//     freshness_status / freshness_class / trigger / evaluated_at and number
//     evidence_count_in_window
//   * scope_cell_id + last_observed_at are nullable (string | null) — the
//     whole-tenant / no-observation row records them as JSON null

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

import { GET } from "../../app/api/controls/[id]/state/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "control-state.golden.json"), "utf8"),
) as Golden;

const CONTROL_ID = "11111111-1111-4111-8111-111111111111";
const ctx = { params: Promise.resolve({ id: CONTROL_ID }) };

describe("contract: GET /api/controls/[id]/state <-> atlas GET /v1/controls/{id}/state", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/{id}/state");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.control_id, `${name}.control_id`).toBe("string");
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(
        Array.isArray(body.states),
        `${name}.states must be an array`,
      ).toBe(true);
      for (const s of body.states as Record<string, unknown>[]) {
        expect(typeof s.result, `${name}.result`).toBe("string");
        expect(typeof s.freshness_status, `${name}.freshness_status`).toBe(
          "string",
        );
        expect(typeof s.freshness_class, `${name}.freshness_class`).toBe(
          "string",
        );
        expect(typeof s.trigger, `${name}.trigger`).toBe("string");
        expect(typeof s.evaluated_at, `${name}.evaluated_at`).toBe("string");
        expect(
          typeof s.evidence_count_in_window,
          `${name}.evidence_count_in_window`,
        ).toBe("number");
        // Nullable fields: present-as-string OR null, never undefined/absent.
        expect("scope_cell_id" in s, `${name}.scope_cell_id present`).toBe(
          true,
        );
        expect(["string", "object"], `${name}.scope_cell_id`).toContain(
          typeof s.scope_cell_id,
        );
        expect(
          "last_observed_at" in s,
          `${name}.last_observed_at present`,
        ).toBe(true);
        expect(["string", "object"], `${name}.last_observed_at`).toContain(
          typeof s.last_observed_at,
        );
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
