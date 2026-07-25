// Slice 411 — contract-test-tier rollout (consumer side: GET
// /v1/audit-periods, audit-workspace period index served via /api/audits).
//
// PROVIDER: internal/api/auditperiods/handler_contract_test.go records the
// real List handler's bodies into audit-periods.golden.json. This CONSUMER
// half asserts the BFF (web/app/api/audits/route.ts) against them. The BFF is
// a VERBATIM passthrough — it forwards the upstream body text unchanged — so
// the assert is toEqual(golden), NOT transform-aware like slice 410's risks
// BFF.
//
// Load-bearing field assumptions (the /audits period-index view):
//   * audit_periods is always an array (never null) — empty set is []
//   * count is a number
//   * each period carries string id/name/framework_version_id/status/
//     created_by, and ISO-string period_start/period_end/created_at/updated_at
//   * frozen_at / frozen_hash / frozen_by are OPTIONAL (absent on open
//     periods — the omitempty branch); when present they are strings

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

import { GET } from "../../app/api/audits/route";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "audit-periods.golden.json"), "utf8"),
) as Golden;

describe("contract: GET /api/audits <-> atlas GET /v1/audit-periods", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/audit-periods");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every provider variant satisfies the period-index field contract", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(
        Array.isArray(body.audit_periods),
        `${name}.audit_periods must be an array`,
      ).toBe(true);
      for (const p of body.audit_periods as Record<string, unknown>[]) {
        expect(typeof p.id, `${name}.id`).toBe("string");
        expect(typeof p.name, `${name}.name`).toBe("string");
        expect(
          typeof p.framework_version_id,
          `${name}.framework_version_id`,
        ).toBe("string");
        expect(typeof p.status, `${name}.status`).toBe("string");
        expect(typeof p.created_by, `${name}.created_by`).toBe("string");
        expect(typeof p.period_start, `${name}.period_start`).toBe("string");
        expect(typeof p.period_end, `${name}.period_end`).toBe("string");
        expect(typeof p.created_at, `${name}.created_at`).toBe("string");
        expect(typeof p.updated_at, `${name}.updated_at`).toBe("string");
        // frozen_* are optional (absent on open periods); typed when present.
        if (p.frozen_at !== undefined) {
          expect(typeof p.frozen_at, `${name}.frozen_at`).toBe("string");
          expect(typeof p.frozen_hash, `${name}.frozen_hash`).toBe("string");
          expect(typeof p.frozen_by, `${name}.frozen_by`).toBe("string");
        }
        // Slice 680 / ATLAS-033: framework_label is optional (omitempty —
        // absent when the framework version no longer resolves); a string
        // when present (e.g. "SCF 2025.2"). The /audits view renders it in
        // place of a truncated framework_version_id UUID.
        if (p.framework_label !== undefined) {
          expect(typeof p.framework_label, `${name}.framework_label`).toBe(
            "string",
          );
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
