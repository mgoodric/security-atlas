// Slice 102 — vitest unit coverage for /audits presentation helpers.
//
// Status-pill color, frozen-icon visibility, days-until-end formatting
// (AC-8). Pure-data tests — no React, no DOM.

import { describe, expect, test } from "vitest";

import type { AuditPeriod } from "@/lib/api";

import {
  IN_PROGRESS_URGENT_DAYS,
  daysUntilEnd,
  daysUntilEndLabel,
  frozenMetaLabel,
  frozenTooltip,
  isFrozen,
  isInProgressUrgent,
  periodRangeLabel,
  statusDotClass,
  statusPillClass,
} from "./format";

function period(overrides: Partial<AuditPeriod> = {}): AuditPeriod {
  return {
    id: "00000000-0000-0000-0000-000000000001",
    name: "test period 01",
    framework_version_id: "00000000-0000-0000-0000-0000000000ff",
    period_start: "2026-01-01T00:00:00Z",
    period_end: "2026-03-31T23:59:59Z",
    status: "open",
    created_by: "test-actor",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("statusPillClass (AC-8)", () => {
  test("open → amber tokens", () => {
    expect(statusPillClass("open")).toContain("amber");
  });

  test("in_progress → amber tokens (same active treatment as open)", () => {
    expect(statusPillClass("in_progress")).toContain("amber");
  });

  test("frozen → sky tokens", () => {
    expect(statusPillClass("frozen")).toContain("sky");
  });

  test("closed → slate tokens", () => {
    expect(statusPillClass("closed")).toContain("slate");
  });

  test("planned → slate tokens", () => {
    expect(statusPillClass("planned")).toContain("slate");
  });

  test("unknown status falls through to slate (never crashes)", () => {
    expect(statusPillClass("definitely_not_a_real_status")).toContain("slate");
  });

  test("status pills use neutral semantic tokens — never vendor-prefixed", () => {
    // P0-A5: no `bg-[#...]` or other vendor-coupled tokens.
    for (const s of ["open", "frozen", "closed", "planned", "in_progress"]) {
      const cls = statusPillClass(s);
      expect(cls).not.toMatch(/#[0-9a-f]{3,8}/);
      expect(cls).not.toMatch(/var\(--/);
    }
  });
});

describe("statusDotClass", () => {
  test("open / in_progress dot pulses", () => {
    expect(statusDotClass("open")).toContain("animate-pulse");
    expect(statusDotClass("in_progress")).toContain("animate-pulse");
  });

  test("frozen dot does NOT pulse (terminal state)", () => {
    expect(statusDotClass("frozen")).not.toContain("animate-pulse");
  });

  test("closed dot does NOT pulse (terminal state)", () => {
    expect(statusDotClass("closed")).not.toContain("animate-pulse");
  });
});

describe("isFrozen (AC-8 frozen-icon visibility)", () => {
  test("status='frozen' → true", () => {
    expect(isFrozen(period({ status: "frozen" }))).toBe(true);
  });

  test("status='open' → false", () => {
    expect(isFrozen(period({ status: "open" }))).toBe(false);
  });

  test("status='in_progress' → false (not yet locked)", () => {
    expect(isFrozen(period({ status: "in_progress" }))).toBe(false);
  });

  test("status='closed' → false (closed != frozen)", () => {
    expect(isFrozen(period({ status: "closed" }))).toBe(false);
  });

  test("unknown status → false (we only lock on exact 'frozen')", () => {
    expect(isFrozen(period({ status: "anything_else" }))).toBe(false);
  });
});

describe("daysUntilEnd (AC-8 formatting)", () => {
  const now = new Date("2026-05-01T00:00:00Z");

  test("positive when period_end is in the future", () => {
    const p = period({ period_end: "2026-05-31T00:00:00Z" });
    expect(daysUntilEnd(p, now)).toBe(30);
  });

  test("zero when period_end is today (or rounded-up)", () => {
    const p = period({ period_end: "2026-05-01T00:00:00Z" });
    expect(daysUntilEnd(p, now)).toBe(0);
  });

  test("negative when period_end has passed", () => {
    const p = period({ period_end: "2026-04-21T00:00:00Z" });
    // 10 days earlier → -10
    expect(daysUntilEnd(p, now)).toBe(-10);
  });

  test("daysUntilEndLabel formats positive as 'Xd left'", () => {
    expect(daysUntilEndLabel(29)).toBe("29d left");
    expect(daysUntilEndLabel(1)).toBe("1d left");
  });

  test("daysUntilEndLabel formats zero as 'ends today'", () => {
    expect(daysUntilEndLabel(0)).toBe("ends today");
  });

  test("daysUntilEndLabel formats negative as 'Xd ago'", () => {
    expect(daysUntilEndLabel(-1)).toBe("1d ago");
    expect(daysUntilEndLabel(-30)).toBe("30d ago");
  });
});

describe("isInProgressUrgent (AC-6 amber-dot cue)", () => {
  const now = new Date("2026-05-01T00:00:00Z");

  test("non-frozen + within 30 days → urgent", () => {
    const p = period({
      status: "open",
      period_end: "2026-05-15T00:00:00Z", // 14 days out
    });
    expect(isInProgressUrgent(p, now)).toBe(true);
  });

  test("non-frozen at exactly 30 days out → urgent (inclusive)", () => {
    const p = period({
      status: "open",
      period_end: "2026-05-31T00:00:00Z", // exactly 30 days
    });
    expect(isInProgressUrgent(p, now)).toBe(true);
  });

  test("non-frozen beyond 30 days → NOT urgent", () => {
    const p = period({
      status: "open",
      period_end: "2026-07-15T00:00:00Z",
    });
    expect(isInProgressUrgent(p, now)).toBe(false);
  });

  test("non-frozen with past period_end → NOT urgent (different signal needed)", () => {
    const p = period({
      status: "open",
      period_end: "2026-04-15T00:00:00Z", // 16 days ago
    });
    expect(isInProgressUrgent(p, now)).toBe(false);
  });

  test("frozen periods are NEVER urgent (lock is the only marker they need)", () => {
    const p = period({
      status: "frozen",
      period_end: "2026-05-10T00:00:00Z", // would be 9 days if not frozen
    });
    expect(isInProgressUrgent(p, now)).toBe(false);
  });

  test("threshold constant matches the slice text (30 days)", () => {
    expect(IN_PROGRESS_URGENT_DAYS).toBe(30);
  });
});

describe("frozenTooltip", () => {
  test("returns '' for non-frozen periods", () => {
    expect(frozenTooltip(period({ status: "open" }))).toBe("");
  });

  test("renders 'frozen at YYYY-MM-DD by <actor>' for frozen periods", () => {
    const p = period({
      status: "frozen",
      frozen_at: "2026-04-03T12:34:56Z",
      frozen_by: "test-actor-sam",
    });
    expect(frozenTooltip(p)).toBe("frozen at 2026-04-03 by test-actor-sam");
  });

  test("renders 'frozen at YYYY-MM-DD' when frozen_by is missing", () => {
    const p = period({
      status: "frozen",
      frozen_at: "2026-04-03T12:34:56Z",
      frozen_by: null,
    });
    expect(frozenTooltip(p)).toBe("frozen at 2026-04-03");
  });

  test("renders generic 'frozen' when frozen_at is missing", () => {
    const p = period({
      status: "frozen",
      frozen_at: null,
      frozen_by: null,
    });
    expect(frozenTooltip(p)).toBe("frozen");
  });
});

describe("periodRangeLabel", () => {
  test("renders 'YYYY-MM-DD → YYYY-MM-DD' from RFC3339 inputs", () => {
    const p = period({
      period_start: "2026-04-01T00:00:00Z",
      period_end: "2026-06-30T23:59:59Z",
    });
    expect(periodRangeLabel(p)).toBe("2026-04-01 → 2026-06-30");
  });
});

describe("frozenMetaLabel", () => {
  test("returns '' for non-frozen periods (cell renders em-dash)", () => {
    expect(frozenMetaLabel(period({ status: "open" }))).toBe("");
  });

  test("renders 'YYYY-MM-DD · <actor>' for frozen periods", () => {
    const p = period({
      status: "frozen",
      frozen_at: "2026-04-03T12:34:56Z",
      frozen_by: "test-actor",
    });
    expect(frozenMetaLabel(p)).toBe("2026-04-03 · test-actor");
  });

  test("renders just the date when frozen_by is missing", () => {
    const p = period({
      status: "frozen",
      frozen_at: "2026-04-03T12:34:56Z",
      frozen_by: null,
    });
    expect(frozenMetaLabel(p)).toBe("2026-04-03");
  });

  test("renders just the actor when frozen_at is missing", () => {
    const p = period({
      status: "frozen",
      frozen_at: null,
      frozen_by: "test-actor",
    });
    expect(frozenMetaLabel(p)).toBe("test-actor");
  });
});
