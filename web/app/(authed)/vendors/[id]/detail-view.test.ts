// Slice 686 — unit tests for the read-only vendor detail view's pure
// formatting helpers (date display, owner mailto resolution, DPA status
// label). The page component itself is exercised by the Playwright e2e
// spec (the project's de-facto component-test tier, slice 353 Q-3); these
// fast pure-function tests cover the branchy formatting logic without a
// DOM.

import { describe, expect, it } from "vitest";

import {
  dpaStatusLabel,
  formatDetailDate,
  ownerMailto,
  reviewOutcomeBadgeVariant,
  reviewOutcomeLabel,
} from "./detail-view";

describe("formatDetailDate", () => {
  it("renders an em-dash for null / undefined / empty", () => {
    expect(formatDetailDate(null)).toBe("—");
    expect(formatDetailDate(undefined)).toBe("—");
    expect(formatDetailDate("")).toBe("—");
  });

  it("trims an ISO timestamp to its date portion", () => {
    expect(formatDetailDate("2026-01-15T09:30:00Z")).toBe("2026-01-15");
  });

  it("passes a bare date through unchanged", () => {
    expect(formatDetailDate("2026-01-15")).toBe("2026-01-15");
  });
});

describe("ownerMailto", () => {
  it("returns a mailto href for a valid email owner", () => {
    expect(ownerMailto("alice@demo.example")).toBe("mailto:alice@demo.example");
  });

  it("trims surrounding whitespace before building the href", () => {
    expect(ownerMailto("  bob@demo.example  ")).toBe("mailto:bob@demo.example");
  });

  it("returns null for a non-email owner (role string)", () => {
    expect(ownerMailto("Head of Security")).toBeNull();
  });

  it("returns null for an empty / nullish owner", () => {
    expect(ownerMailto("")).toBeNull();
    expect(ownerMailto(null)).toBeNull();
    expect(ownerMailto(undefined)).toBeNull();
  });
});

describe("dpaStatusLabel", () => {
  it("reports a signed DPA with its signing date when present", () => {
    expect(dpaStatusLabel(true, "2026-02-01T00:00:00Z")).toBe(
      "Signed (2026-02-01)",
    );
  });

  it("reports a signed DPA without a date when the date is absent", () => {
    expect(dpaStatusLabel(true, null)).toBe("Signed");
    expect(dpaStatusLabel(true, "")).toBe("Signed");
  });

  it("reports an unsigned DPA and ignores any stray date", () => {
    expect(dpaStatusLabel(false, null)).toBe("Not signed");
    expect(dpaStatusLabel(false, "2026-02-01")).toBe("Not signed");
  });
});

describe("reviewOutcomeLabel", () => {
  it("renders each known outcome as a human-readable label", () => {
    expect(reviewOutcomeLabel("pass")).toBe("Pass");
    expect(reviewOutcomeLabel("pass_with_findings")).toBe("Pass with findings");
    expect(reviewOutcomeLabel("fail")).toBe("Fail");
    expect(reviewOutcomeLabel("waived")).toBe("Waived");
  });

  it("falls back to the raw string for an unknown outcome", () => {
    expect(reviewOutcomeLabel("remediated")).toBe("remediated");
    expect(reviewOutcomeLabel("")).toBe("");
  });
});

describe("reviewOutcomeBadgeVariant", () => {
  it("maps a fail to the destructive variant", () => {
    expect(reviewOutcomeBadgeVariant("fail")).toBe("destructive");
  });

  it("maps findings / waiver to the outline variant", () => {
    expect(reviewOutcomeBadgeVariant("pass_with_findings")).toBe("outline");
    expect(reviewOutcomeBadgeVariant("waived")).toBe("outline");
  });

  it("maps a clean pass (and unknowns) to the secondary variant", () => {
    expect(reviewOutcomeBadgeVariant("pass")).toBe("secondary");
    expect(reviewOutcomeBadgeVariant("something-new")).toBe("secondary");
  });
});
