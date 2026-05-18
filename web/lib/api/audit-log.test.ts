// Slice 125 — vitest cases for the audit-log typed client helpers.

import { describe, expect, test } from "vitest";

import {
  AUDIT_LOG_KINDS,
  AuditLogFetchError,
  MAX_WINDOW_DAYS,
  buildUnifiedQuery,
} from "./audit-log";

describe("buildUnifiedQuery", () => {
  test("emits the required from/to params verbatim", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
    });
    expect(q).toContain("from=2026-05-11T00%3A00%3A00.000Z");
    expect(q).toContain("to=2026-05-18T00%3A00%3A00.000Z");
    expect(q.startsWith("?")).toBe(true);
  });

  test("drops empty optional fields", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      actor: "",
      kinds: [],
      cursor: "",
    });
    expect(q).not.toContain("actor");
    expect(q).not.toContain("kind=");
    expect(q).not.toContain("cursor");
  });

  test("joins kinds with commas (matches backend csv parser)", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      kinds: ["evidence", "me", "walkthrough"],
    });
    expect(q).toContain("kind=evidence%2Cme%2Cwalkthrough");
  });

  test("trims actor input (defensive against UI whitespace)", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      actor: "  00000000-0000-0000-0000-000000001111  ",
    });
    expect(q).toContain("actor=00000000-0000-0000-0000-000000001111");
  });

  test("passes cursor through when set", () => {
    const q = buildUnifiedQuery({
      from: "2026-05-11T00:00:00.000Z",
      to: "2026-05-18T00:00:00.000Z",
      cursor: "opaquebase64token",
    });
    expect(q).toContain("cursor=opaquebase64token");
  });
});

describe("AUDIT_LOG_KINDS", () => {
  test("enumerates exactly the nine canonical kinds (matches platform unifiedlog.Kind)", () => {
    expect(AUDIT_LOG_KINDS).toEqual([
      "decision",
      "evidence",
      "exception",
      "sample",
      "audit_period",
      "aggregation_rule",
      "feature_flag",
      "me",
      "walkthrough",
    ]);
  });
});

describe("MAX_WINDOW_DAYS", () => {
  test("matches the backend's 90-day cap (defense-in-depth single source of truth)", () => {
    // Backend: internal/api/adminauditlog/unified.go const maxWindowDays = 90.
    expect(MAX_WINDOW_DAYS).toBe(90);
  });
});

describe("AuditLogFetchError", () => {
  test("carries the upstream status code for the UI to branch on", () => {
    const err = new AuditLogFetchError(
      403,
      "admin, auditor, or grc_engineer role required",
    );
    expect(err.status).toBe(403);
    expect(err.message).toMatch(/admin, auditor/);
    expect(err.name).toBe("AuditLogFetchError");
  });
});
