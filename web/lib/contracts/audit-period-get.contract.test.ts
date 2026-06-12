// Slice 687 — contract-test-tier rollout (consumer side: GET
// /v1/audit-periods/{id}, the single audit-period read).
//
// PROVIDER: internal/api/auditperiods/handler687_contract_test.go records the
// real Get handler's bodies into audit-period-get.golden.json.
//
// CONSUMER SHAPE (honest scoping — slice 687 D3): unlike /coverage, there is
// NO current Next.js BFF that consumes GET /v1/audit-periods/{id} — the
// audit-workspace reads the caller-scoped GET /v1/me/audit-period instead
// (web/lib/api/audit.ts getAuditPeriod). So this CONSUMER half is a
// FIELD-CONTRACT assert on the recorded provider golden, NOT a BFF-passthrough
// drive: it pins the load-bearing wire-shape assumptions a future
// single-period BFF will depend on, and (paired with the Go provider recorder)
// it fails on provider drift. When a single-period BFF lands, the
// passthrough-drive half is added here (tracked in slice 687 D3 + the
// audit-period-get spillover note).
//
// Load-bearing field assumptions (the AuditPeriod wire shape in
// internal/api/auditperiods/handlers.go periodWire):
//   * audit_period is always present (object)
//   * id / name / framework_version_id / status / created_by are strings
//   * period_start / period_end / created_at / updated_at are strings (ISO-8601)
//   * frozen_at / frozen_hash / frozen_by are omitempty — ABSENT on open
//     periods, PRESENT on frozen periods (frozen_hash is hex-encoded)
//   * framework_label is omitempty and ABSENT here: the single-period Get does
//     NOT join the catalog label (slice 680 — that join is List-only)

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, test } from "vitest";

interface Golden {
  endpoint: string;
  variants: Record<string, { audit_period: Record<string, unknown> }>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "audit-period-get.golden.json"), "utf8"),
) as Golden;

describe("contract: atlas GET /v1/audit-periods/{id} provider shape", () => {
  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/audit-periods/{id}");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every variant carries an audit_period object with the core string fields", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      const p = body.audit_period;
      expect(typeof p, `${name}.audit_period`).toBe("object");
      for (const field of [
        "id",
        "name",
        "framework_version_id",
        "status",
        "created_by",
        "period_start",
        "period_end",
        "created_at",
        "updated_at",
      ]) {
        expect(typeof p[field], `${name}.${field}`).toBe("string");
      }
      // framework_label is List-only (slice 680) — absent on the single Get.
      expect("framework_label" in p, `${name}.framework_label absent`).toBe(
        false,
      );
    }
  });

  test("open period omits the frozen_* fields (omitempty)", () => {
    const open = golden.variants.open?.audit_period;
    expect(open, "open variant present").toBeDefined();
    expect(open.status).toBe("open");
    expect("frozen_at" in open, "open.frozen_at absent").toBe(false);
    expect("frozen_hash" in open, "open.frozen_hash absent").toBe(false);
    expect("frozen_by" in open, "open.frozen_by absent").toBe(false);
  });

  test("frozen period carries frozen_at + hex frozen_hash + frozen_by", () => {
    const frozen = golden.variants.frozen?.audit_period;
    expect(frozen, "frozen variant present").toBeDefined();
    expect(frozen.status).toBe("frozen");
    expect(typeof frozen.frozen_at, "frozen.frozen_at").toBe("string");
    expect(typeof frozen.frozen_by, "frozen.frozen_by").toBe("string");
    expect(typeof frozen.frozen_hash, "frozen.frozen_hash").toBe("string");
    // Hex-encoded SHA-256 = 64 lowercase hex chars.
    expect(frozen.frozen_hash as string).toMatch(/^[0-9a-f]{64}$/);
  });
});
