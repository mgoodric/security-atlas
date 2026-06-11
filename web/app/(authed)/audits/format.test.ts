// Slice 102 — vitest unit coverage for /audits presentation helpers.
//
// Status-pill color, frozen-icon visibility, days-until-end formatting
// (AC-8). Pure-data tests — no React, no DOM.

import { describe, expect, test } from "vitest";

import type { AuditPeriod } from "@/lib/api/audit-periods";

import {
  IN_PROGRESS_URGENT_DAYS,
  TALLY_STATUS_ORDER,
  daysUntilEnd,
  daysUntilEndLabel,
  frameworkVersionLabel,
  frozenMetaLabel,
  frozenTooltip,
  isFrozen,
  isInProgressUrgent,
  periodRangeLabel,
  statusDotClass,
  statusPillClass,
  statusTallyLabel,
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

describe("statusTallyLabel (slice 215 AC-4)", () => {
  test("AC-2: empty periods list → empty string", () => {
    expect(statusTallyLabel([])).toBe("");
  });

  test("AC-1: all four canonical statuses render in prescribed order", () => {
    // One of each canonical status. Insertion order intentionally
    // scrambled so the assertion verifies ordering by TALLY_STATUS_ORDER
    // (in_progress · frozen · closed · open), not by data arrival.
    const periods = [
      period({ id: "1", status: "closed" }),
      period({ id: "2", status: "open" }),
      period({ id: "3", status: "in_progress" }),
      period({ id: "4", status: "frozen" }),
    ];
    expect(statusTallyLabel(periods)).toBe(
      "1 in_progress · 1 frozen · 1 closed · 1 open",
    );
  });

  test("AC-4 single-status case: only that status renders ('6 frozen')", () => {
    const periods = Array.from({ length: 6 }, (_, i) =>
      period({ id: `${i}`, status: "frozen" }),
    );
    expect(statusTallyLabel(periods)).toBe("6 frozen");
  });

  test("AC-1 mockup case: '1 in progress · 4 frozen · 1 closed'", () => {
    // Spec mockup text uses spaces in 'in progress'; the platform's
    // enum is `in_progress` (underscore) and that's what we render
    // verbatim per AI-assist tone discipline + invariant 10.
    const periods = [
      period({ id: "1", status: "in_progress" }),
      ...Array.from({ length: 4 }, (_, i) =>
        period({ id: `f${i}`, status: "frozen" }),
      ),
      period({ id: "9", status: "closed" }),
    ];
    expect(statusTallyLabel(periods)).toBe(
      "1 in_progress · 4 frozen · 1 closed",
    );
  });

  test("P0-215-1: statuses with count 0 are OMITTED", () => {
    // Only frozen periods present — 'closed', 'open', 'in_progress'
    // must NOT appear in the rendered string.
    const periods = Array.from({ length: 3 }, (_, i) =>
      period({ id: `${i}`, status: "frozen" }),
    );
    const tally = statusTallyLabel(periods);
    expect(tally).toBe("3 frozen");
    expect(tally).not.toContain("0 ");
    expect(tally).not.toContain("closed");
    expect(tally).not.toContain("open");
    expect(tally).not.toContain("in_progress");
  });

  test("P0-215-2: only statuses present in periods[].status render — never invent", () => {
    // 'planned' is in the platform's forward-looking enum but no
    // period carries it here; it must NOT appear in the rendered
    // string. Same for 'open' / 'in_progress'.
    const periods = [
      period({ id: "1", status: "frozen" }),
      period({ id: "2", status: "closed" }),
    ];
    const tally = statusTallyLabel(periods);
    expect(tally).not.toContain("planned");
    expect(tally).not.toContain("open");
    expect(tally).not.toContain("in_progress");
  });

  test("non-canonical statuses (e.g. 'planned') appear after canonical four, alphabetically", () => {
    // Once the backend lifts the CHECK constraint and 'planned' shows
    // up, it should render — just appended after the canonical four
    // in alphabetical order for deterministic output.
    const periods = [
      period({ id: "1", status: "frozen" }),
      period({ id: "2", status: "planned" }),
      period({ id: "3", status: "in_progress" }),
      period({ id: "4", status: "experimental" }),
    ];
    expect(statusTallyLabel(periods)).toBe(
      "1 in_progress · 1 frozen · 1 experimental · 1 planned",
    );
  });

  test("uses ' · ' (U+00B7 MIDDLE DOT) as separator — matches mockup verbatim", () => {
    const periods = [
      period({ id: "1", status: "frozen" }),
      period({ id: "2", status: "closed" }),
    ];
    const tally = statusTallyLabel(periods);
    // Exact mockup separator: space + middle-dot + space.
    expect(tally).toContain(" · ");
    // And NOT the comma/bullet alternatives some designers reach for.
    expect(tally).not.toContain(", ");
    expect(tally).not.toContain(" • ");
  });

  test("TALLY_STATUS_ORDER constant matches the slice 215 AC-1 order", () => {
    expect(TALLY_STATUS_ORDER).toEqual([
      "in_progress",
      "frozen",
      "closed",
      "open",
    ]);
  });

  test("handles many periods of mixed statuses (sanity)", () => {
    // 100 frozen + 50 open + 5 closed + 2 in_progress.
    const periods = [
      ...Array.from({ length: 100 }, (_, i) =>
        period({ id: `f${i}`, status: "frozen" }),
      ),
      ...Array.from({ length: 50 }, (_, i) =>
        period({ id: `o${i}`, status: "open" }),
      ),
      ...Array.from({ length: 5 }, (_, i) =>
        period({ id: `c${i}`, status: "closed" }),
      ),
      ...Array.from({ length: 2 }, (_, i) =>
        period({ id: `i${i}`, status: "in_progress" }),
      ),
    ];
    expect(statusTallyLabel(periods)).toBe(
      "2 in_progress · 100 frozen · 5 closed · 50 open",
    );
  });
});

describe("frameworkVersionLabel (slice 680 / ATLAS-033)", () => {
  test("renders the readable label when framework_label is present", () => {
    const got = frameworkVersionLabel(
      period({ framework_label: "SCF 2025.2" }),
    );
    expect(got).toEqual({ text: "SCF 2025.2", readable: true });
  });

  test("falls back to the truncated UUID when framework_label is absent", () => {
    const got = frameworkVersionLabel(
      period({
        framework_version_id: "e443f4b1-0000-0000-0000-000000000000",
        framework_label: undefined,
      }),
    );
    expect(got.readable).toBe(false);
    expect(got.text).toBe("e443f4b1…");
  });

  test("treats a whitespace-only label as absent (fallback to UUID)", () => {
    const got = frameworkVersionLabel(
      period({
        framework_version_id: "abcdef12-0000-0000-0000-000000000000",
        framework_label: "   ",
      }),
    );
    expect(got.readable).toBe(false);
    expect(got.text).toBe("abcdef12…");
  });

  test("trims surrounding whitespace from a real label", () => {
    const got = frameworkVersionLabel(
      period({ framework_label: "  ISO 27001 2022 " }),
    );
    expect(got).toEqual({ text: "ISO 27001 2022", readable: true });
  });
});
