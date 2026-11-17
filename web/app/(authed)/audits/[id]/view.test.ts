// #2117 — unit tests for the /audits/[id] view helpers.

import { describe, expect, test } from "vitest";

import { APIError } from "@/lib/api/base";
import type { AuditPeriod } from "@/lib/api/audit-periods";

import {
  auditorChangeErrorMessage,
  auditorLabel,
  FREEZE_CONFIRM_DETAIL,
  freezeErrorMessage,
  isOscalBridgeUnavailable,
  OSCAL_UNAVAILABLE_MESSAGE,
  oscalExportErrorMessage,
  PERIOD_NOT_FOUND_CODE,
  summarizeControlState,
  toWorkspacePeriod,
} from "./view";

describe("summarizeControlState", () => {
  test("no observations: zero count, no latest", () => {
    expect(
      summarizeControlState({
        audit_period_id: "p",
        control_id: "c",
        observations: [],
        count: 0,
      }),
    ).toEqual({ count: 0, latestResult: null, latestObservedAt: null });
  });

  test("picks the latest observation regardless of order", () => {
    expect(
      summarizeControlState({
        audit_period_id: "p",
        control_id: "c",
        observations: [
          {
            evidence_record_id: "e1",
            observed_at: "2026-02-01T00:00:00Z",
            result: "pass",
            hash: "h1",
          },
          {
            evidence_record_id: "e2",
            observed_at: "2026-03-01T00:00:00Z",
            result: "fail",
            hash: "h2",
          },
          {
            evidence_record_id: "e3",
            observed_at: "2026-01-01T00:00:00Z",
            result: "pass",
            hash: "h3",
          },
        ],
        count: 3,
      }),
    ).toEqual({
      count: 3,
      latestResult: "fail",
      latestObservedAt: "2026-03-01T00:00:00Z",
    });
  });
});

describe("freeze confirm copy", () => {
  test("says the evidence becomes immutable", () => {
    expect(FREEZE_CONFIRM_DETAIL).toMatch(/evidence becomes immutable/);
  });
});

describe("freezeErrorMessage", () => {
  test("403 reads as a permission refusal", () => {
    expect(freezeErrorMessage(new APIError(403, "forbidden"))).toMatch(
      /permission/,
    );
  });
  test("409 reads as already frozen", () => {
    expect(freezeErrorMessage(new APIError(409, "x"))).toMatch(
      /already frozen/,
    );
  });
  test("other statuses carry the upstream message", () => {
    expect(freezeErrorMessage(new APIError(500, "boom"))).toMatch(/boom/);
  });
  test("a non-API error still gets a message", () => {
    expect(freezeErrorMessage(new Error("x"))).toMatch(/Could not freeze/);
  });
});

describe("isOscalBridgeUnavailable", () => {
  test("a bare 404 means the export route is not mounted", () => {
    expect(isOscalBridgeUnavailable(new APIError(404, "404 Not Found"))).toBe(
      true,
    );
  });
  test("the period's own 404 is not the bridge", () => {
    expect(
      isOscalBridgeUnavailable(
        new APIError(404, "audit period not found", PERIOD_NOT_FOUND_CODE),
      ),
    ).toBe(false);
  });
  test("the period 404 is matched on its code, not its message (#2312)", () => {
    expect(
      isOscalBridgeUnavailable(
        new APIError(404, "no such period", PERIOD_NOT_FOUND_CODE),
      ),
    ).toBe(false);
    expect(
      isOscalBridgeUnavailable(new APIError(404, "audit period not found")),
    ).toBe(true);
  });
  test("502 and 503 read as the bridge being down", () => {
    expect(isOscalBridgeUnavailable(new APIError(502, "x"))).toBe(true);
    expect(isOscalBridgeUnavailable(new APIError(503, "x"))).toBe(true);
  });
  test("a 409 (not frozen) is a refusal, not the bridge", () => {
    expect(isOscalBridgeUnavailable(new APIError(409, "x"))).toBe(false);
    expect(oscalExportErrorMessage(new APIError(409, "not frozen"))).toMatch(
      /not frozen/,
    );
  });
  test("the unavailable message names the bridge", () => {
    expect(oscalExportErrorMessage(new APIError(404, "404 Not Found"))).toBe(
      OSCAL_UNAVAILABLE_MESSAGE,
    );
  });
});

describe("toWorkspacePeriod", () => {
  test("maps the period id and keeps the freeze horizon", () => {
    const p: AuditPeriod = {
      id: "p1",
      name: "SOC 2 2026",
      framework_version_id: "fv",
      period_start: "2026-01-01T00:00:00Z",
      period_end: "2026-03-31T00:00:00Z",
      status: "frozen",
      frozen_at: "2026-04-01T00:00:00Z",
      created_by: "u",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-04-01T00:00:00Z",
    };
    expect(toWorkspacePeriod(p)).toMatchObject({
      audit_period_id: "p1",
      status: "frozen",
      frozen_at: "2026-04-01T00:00:00Z",
    });
  });
});

describe("auditorLabel", () => {
  test("name and email together", () => {
    expect(
      auditorLabel({
        user_id: "u",
        display_name: "Ada",
        email: "ada@example.invalid",
      }),
    ).toBe("Ada (ada@example.invalid)");
  });
  test("falls back to the email, then the id", () => {
    expect(
      auditorLabel({ user_id: "u", display_name: "", email: "e@x.invalid" }),
    ).toBe("e@x.invalid");
    expect(auditorLabel({ user_id: "u-1", display_name: "", email: "" })).toBe(
      "u-1",
    );
  });
});

describe("auditorChangeErrorMessage", () => {
  test("403 says only an admin can change auditors", () => {
    expect(
      auditorChangeErrorMessage(new APIError(403, "forbidden"), "assign"),
    ).toMatch(/Only an admin/);
  });
  test("404 on remove says the grant is gone", () => {
    expect(auditorChangeErrorMessage(new APIError(404, "x"), "remove")).toMatch(
      /no longer assigned/,
    );
  });
  test("404 on assign says the period is gone", () => {
    expect(auditorChangeErrorMessage(new APIError(404, "x"), "assign")).toMatch(
      /period no longer exists/,
    );
  });
  test("other statuses carry the platform message", () => {
    expect(
      auditorChangeErrorMessage(
        new APIError(400, "user_id is not an active auditor in this tenant"),
        "assign",
      ),
    ).toMatch(/not an active auditor/);
  });
});
