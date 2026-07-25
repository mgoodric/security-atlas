// Slice 690 — contract-test-tier rollout (consumer side: GET /v1/walkthroughs,
// the tenant walkthrough list).
//
// PROVIDER: internal/api/walkthroughs/contractrecord_test.go records the real
// List handler's bodies into walkthroughs-list.golden.json.
//
// CONSUMER SHAPE (honest scoping — slice 687 D3): the list BFF
// (web/app/api/audit/walkthroughs/route.ts) is POST-only today — there is NO
// GET consumer. So this CONSUMER half is a FIELD-CONTRACT assert on the
// recorded provider golden, NOT a BFF-passthrough drive: it pins the
// load-bearing wire-shape assumptions a future list-GET BFF will depend on, and
// (paired with the Go provider recorder) it fails on provider drift. When a
// list GET BFF lands, the passthrough-drive half is added here.
//
// Load-bearing field assumptions (the envelope built by
// internal/api/walkthroughs/handlers.go List + toWire):
//   * walkthroughs is ALWAYS an array (never null); empty list records []
//   * count is a number and equals walkthroughs.length
//   * each row carries id / control_id / narrative / status / canonical_hash /
//     created_by (strings), created_at / updated_at (ISO-8601 strings), and
//     tamper_detected (always present boolean)
//   * canonical_hash is hex-encoded (64 lowercase hex chars)
//   * audit_period_id is omitempty — PRESENT on a period-pinned walkthrough,
//     ABSENT otherwise

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, test } from "vitest";

interface WalkthroughRow {
  id: string;
  control_id: string;
  narrative: string;
  status: string;
  canonical_hash: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  tamper_detected: boolean;
  audit_period_id?: string;
}

interface Golden {
  endpoint: string;
  variants: Record<string, { walkthroughs: WalkthroughRow[]; count: number }>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "walkthroughs-list.golden.json"), "utf8"),
) as Golden;

describe("contract: atlas GET /v1/walkthroughs provider shape", () => {
  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/walkthroughs");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every variant envelope is {walkthroughs:[], count} with count === length", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        Array.isArray(body.walkthroughs),
        `${name}.walkthroughs is array`,
      ).toBe(true);
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(body.count, `${name}.count === length`).toBe(
        body.walkthroughs.length,
      );
    }
  });

  test("every walkthrough row carries the core fields + hex canonical_hash + tamper flag", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      for (const [i, row] of body.walkthroughs.entries()) {
        for (const field of [
          "id",
          "control_id",
          "narrative",
          "status",
          "canonical_hash",
          "created_by",
          "created_at",
          "updated_at",
        ] as const) {
          expect(typeof row[field], `${name}[${i}].${field}`).toBe("string");
        }
        expect(
          typeof row.tamper_detected,
          `${name}[${i}].tamper_detected`,
        ).toBe("boolean");
        // Hex-encoded SHA-256 = 64 lowercase hex chars.
        expect(row.canonical_hash, `${name}[${i}].canonical_hash hex`).toMatch(
          /^[0-9a-f]{64}$/,
        );
      }
    }
  });

  test("empty list records [] and count 0", () => {
    const empty = golden.variants.empty;
    expect(empty, "empty variant present").toBeDefined();
    expect(empty.walkthroughs).toEqual([]);
    expect(empty.count).toBe(0);
  });

  test("audit_period_id is present on a pinned row, absent on an unpinned row (omitempty)", () => {
    const populated = golden.variants.populated;
    expect(populated, "populated variant present").toBeDefined();
    const pinned = populated.walkthroughs.find((w) => "audit_period_id" in w);
    const unpinned = populated.walkthroughs.find(
      (w) => !("audit_period_id" in w),
    );
    expect(pinned, "a period-pinned row exists").toBeDefined();
    expect(typeof pinned?.audit_period_id, "pinned.audit_period_id").toBe(
      "string",
    );
    expect(unpinned, "an unpinned row exists").toBeDefined();
  });
});
