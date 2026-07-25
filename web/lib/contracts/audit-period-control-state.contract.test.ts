// Slice 687 — contract-test-tier rollout (consumer side: GET
// /v1/audit-periods/{id}/control-state, the frozen-horizon observation read).
//
// PROVIDER: internal/api/auditperiods/handler687_contract_test.go records the
// real ControlState handler's bodies into audit-period-control-state.golden.json.
//
// CONSUMER SHAPE (honest scoping — slice 687 D3): like the single-period Get,
// there is NO current Next.js BFF consuming this route. This CONSUMER half is a
// FIELD-CONTRACT assert on the recorded provider golden: it pins the
// load-bearing wire-shape assumptions a future audit-sampling BFF will depend
// on, and (paired with the Go provider recorder) fails on provider drift. The
// passthrough-drive half is added when the BFF lands (slice 687 D3 spillover).
//
// Load-bearing field assumptions (the control-state wire shape in
// internal/api/auditperiods/handlers.go ControlState + controlStateObservationWire):
//   * audit_period_id / control_id are strings; count is a number
//   * observations is ALWAYS an array (never null) — empty horizon records []
//   * each observation carries string evidence_record_id / result / hash and a
//     string observed_at (ISO-8601). observations[0] is the most-recent
//     pass/fail-driving record (auditor-facing ordering)

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, test } from "vitest";

interface Golden {
  endpoint: string;
  variants: Record<string, Record<string, unknown>>;
}

const golden: Golden = JSON.parse(
  readFileSync(
    join(__dirname, "audit-period-control-state.golden.json"),
    "utf8",
  ),
) as Golden;

describe("contract: atlas GET /v1/audit-periods/{id}/control-state provider shape", () => {
  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/audit-periods/{id}/control-state");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every variant carries the envelope fields + an observations array", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.audit_period_id, `${name}.audit_period_id`).toBe(
        "string",
      );
      expect(typeof body.control_id, `${name}.control_id`).toBe("string");
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(
        Array.isArray(body.observations),
        `${name}.observations must be an array`,
      ).toBe(true);
      for (const o of body.observations as Record<string, unknown>[]) {
        expect(typeof o.evidence_record_id, `${name}.evidence_record_id`).toBe(
          "string",
        );
        expect(typeof o.observed_at, `${name}.observed_at`).toBe("string");
        expect(typeof o.result, `${name}.result`).toBe("string");
        expect(typeof o.hash, `${name}.hash`).toBe("string");
      }
    }
  });

  test("empty horizon records observations:[] + count 0 (never null)", () => {
    const empty = golden.variants.empty;
    expect(empty, "empty variant present").toBeDefined();
    expect(empty.observations).toEqual([]);
    expect(empty.count).toBe(0);
  });

  test("populated count matches the observations length", () => {
    const populated = golden.variants.populated;
    expect(populated, "populated variant present").toBeDefined();
    expect((populated.observations as unknown[]).length).toBe(populated.count);
  });
});
