// Slice 411 — contract-test-tier rollout (consumer side: GET
// /v1/controls/{id}/history, control-detail audit-log card).
//
// PROVIDER: internal/api/controldetail/handler_contract_test.go records the
// real History handler's bodies into control-history.golden.json. This
// CONSUMER half asserts the BFF (web/app/api/controls/[id]/history/route.ts)
// against them. The BFF is a VERBATIM passthrough: getControlHistory
// (web/lib/api/control-detail.ts) returns res.json() unchanged and the route
// does NextResponse.json(body) — so the assert is toEqual(golden).
//
// Load-bearing field assumptions (ControlHistoryResponse in
// web/lib/api/control-detail.ts):
//   * history is always an array (never null) — empty set is []
//   * count / next_cursor present (next_cursor is a string, "" when no next)
//   * each entry carries string evaluated_at/computed_state/freshness_status,
//     number evidence_count, and scope_cell is string-or-null

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

import { GET } from "../../app/api/controls/[id]/history/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "control-history.golden.json"), "utf8"),
) as Golden;

const CONTROL_ID = "11111111-1111-4111-8111-111111111111";
const ctx = { params: Promise.resolve({ id: CONTROL_ID }) };

describe("contract: GET /api/controls/[id]/history <-> atlas GET /v1/controls/{id}/history", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/controls/{id}/history");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the BFF field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.control_id, `${name}.control_id`).toBe("string");
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(typeof body.next_cursor, `${name}.next_cursor`).toBe("string");
      expect(
        Array.isArray(body.history),
        `${name}.history must be an array`,
      ).toBe(true);
      for (const ev of body.history as Record<string, unknown>[]) {
        expect(typeof ev.evaluated_at, `${name}.evaluated_at`).toBe("string");
        expect(typeof ev.computed_state, `${name}.computed_state`).toBe(
          "string",
        );
        expect(typeof ev.freshness_status, `${name}.freshness_status`).toBe(
          "string",
        );
        expect(typeof ev.evidence_count, `${name}.evidence_count`).toBe(
          "number",
        );
        // scope_cell is string-or-null (never absent).
        expect("scope_cell" in ev, `${name}.scope_cell`).toBe(true);
        if (ev.scope_cell !== null) {
          expect(typeof ev.scope_cell, `${name}.scope_cell`).toBe("string");
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
