// Slice 662 — board-pack section labels.
//
// The board-pack review page must NEVER show a raw internal section key
// (e.g. `vendor_burndown`) to the user — not in a section header, and not
// in the "not ready to publish" blocker list. SECTION_TITLES is the FE
// source of truth for a section's human label, and sectionLabel() resolves
// a label even when the served section is missing or carries an empty
// title (an incomplete stored pack). These pure tests pin both.

import { describe, expect, test } from "vitest";

import {
  BOARD_PACK_SECTION_KEYS,
  BoardPackSection,
  SECTION_TITLES,
  sectionLabel,
} from "./board";

function section(overrides: Partial<BoardPackSection>): BoardPackSection {
  return {
    key: "vendor_burndown",
    title: "Vendor risk burndown",
    templated_text: "",
    override_text: "",
    approved: false,
    data: {},
    ...overrides,
  };
}

describe("SECTION_TITLES", () => {
  test("maps every fixed section key to a human label", () => {
    for (const key of BOARD_PACK_SECTION_KEYS) {
      expect(SECTION_TITLES[key], `missing label for ${key}`).toBeTruthy();
      // A label is human-readable, never the raw snake_case key.
      expect(SECTION_TITLES[key]).not.toBe(key);
    }
  });

  test("vendor_burndown resolves to its board-facing label", () => {
    expect(SECTION_TITLES.vendor_burndown).toBe("Vendor risk burndown");
  });

  test("has no labels beyond the eight fixed keys", () => {
    expect(Object.keys(SECTION_TITLES).sort()).toEqual(
      [...BOARD_PACK_SECTION_KEYS].sort(),
    );
  });
});

describe("sectionLabel", () => {
  test("prefers the canonical FE label", () => {
    expect(sectionLabel("vendor_burndown")).toBe("Vendor risk burndown");
  });

  test("returns the human label even when the section is MISSING", () => {
    // AC-2: a missing vendor_burndown section must still resolve a label,
    // never the raw key.
    expect(sectionLabel("vendor_burndown", undefined)).toBe(
      "Vendor risk burndown",
    );
    expect(sectionLabel("vendor_burndown", undefined)).not.toBe(
      "vendor_burndown",
    );
  });

  test("returns the human label even when the served title is EMPTY", () => {
    const s = section({ title: "" });
    expect(sectionLabel("vendor_burndown", s)).toBe("Vendor risk burndown");
    expect(sectionLabel("vendor_burndown", s)).not.toBe("vendor_burndown");
  });

  test("falls back to the served title for an unknown key", () => {
    const s = section({ key: "future_section", title: "Future section" });
    expect(sectionLabel("future_section", s)).toBe("Future section");
  });

  test("falls back to the raw key only when nothing else is available", () => {
    // Defensive floor — the fixed set never contains an unknown key, but
    // the resolver must not throw or render `undefined`.
    expect(sectionLabel("future_section", undefined)).toBe("future_section");
  });
});

// publishBlockerLabels mirrors the page's approvalState() label logic:
// every missing-or-unapproved section contributes its HUMAN label to the
// blocker list. This is the pure core of AC-2 — exercised here so a raw
// key can never leak into the "not ready to publish" alert.
function publishBlockerLabels(
  sections: Record<string, BoardPackSection>,
): string[] {
  const out: string[] = [];
  for (const key of BOARD_PACK_SECTION_KEYS) {
    const s = sections[key];
    if (!s || !s.approved) out.push(sectionLabel(key, s));
  }
  return out;
}

describe("publish blocker labels", () => {
  test("a MISSING vendor_burndown yields the human label, never the key", () => {
    const sections: Record<string, BoardPackSection> = {};
    for (const key of BOARD_PACK_SECTION_KEYS) {
      if (key === "vendor_burndown") continue; // omit §05 entirely
      sections[key] = section({
        key,
        title: SECTION_TITLES[key],
        approved: true,
      });
    }
    const labels = publishBlockerLabels(sections);
    expect(labels).toEqual(["Vendor risk burndown"]);
    expect(labels).not.toContain("vendor_burndown");
  });

  test("an EMPTY-title unapproved vendor_burndown yields the human label", () => {
    const sections: Record<string, BoardPackSection> = {};
    for (const key of BOARD_PACK_SECTION_KEYS) {
      sections[key] = section({
        key,
        title: key === "vendor_burndown" ? "" : SECTION_TITLES[key],
        approved: key !== "vendor_burndown",
      });
    }
    const labels = publishBlockerLabels(sections);
    expect(labels).toEqual(["Vendor risk burndown"]);
    expect(labels.join(" ")).not.toContain("vendor_burndown");
  });

  test("no blockers when every section is present and approved", () => {
    const sections: Record<string, BoardPackSection> = {};
    for (const key of BOARD_PACK_SECTION_KEYS) {
      sections[key] = section({
        key,
        title: SECTION_TITLES[key],
        approved: true,
      });
    }
    expect(publishBlockerLabels(sections)).toEqual([]);
  });

  test("no blocker is ever a raw snake_case key", () => {
    const labels = publishBlockerLabels({});
    expect(labels).toHaveLength(BOARD_PACK_SECTION_KEYS.length);
    for (const label of labels) {
      expect(label).not.toMatch(/^[a-z_]+$/);
    }
  });
});
