// Slice 223 — vitest coverage for the pure helpers that power
// `<GlobalSearch />`.
//
// The integrated component is hostile to a node-env unit test (DOM
// event listeners, useRouter, fetch). The Playwright e2e spec covers
// the integrated path; the pure helpers covered here pin the
// regression-prone bits:
//
//   - groupByType: the partitioning of mixed-type hits into the three
//     render buckets. A future addition of a fourth type that we
//     forget to handle would silently drop matches; the table-driven
//     coverage here pins the current contract.
//   - hrefForHit: the routing convention for each type. Controls
//     have a real detail page; risks + evidence use the alias surfaces
//     that match the rest of the app. A regression here would break
//     the keyboard-Enter UX.
//   - isShortcutTrigger: the keyboard-event predicate for ⌘K /
//     Ctrl+K. A regression would break the AC-1 ⌘K-focuses-input
//     contract.

import { describe, expect, test } from "vitest";

import {
  groupByType,
  hrefForHit,
  isShortcutTrigger,
} from "./global-search";

interface Hit {
  id: string;
  type: "controls" | "risks" | "evidence";
  title: string;
  snippet: string;
  relevance_score: number;
}

function mkHit(overrides: Partial<Hit> = {}): Hit {
  return {
    id: "00000000-0000-0000-0000-000000000000",
    type: "controls",
    title: "Test hit",
    snippet: "Test snippet",
    relevance_score: 0.5,
    ...overrides,
  };
}

describe("groupByType", () => {
  test("partitions an empty list to three empty buckets", () => {
    expect(groupByType([])).toEqual({
      controls: [],
      risks: [],
      evidence: [],
    });
  });

  test("partitions a mixed-type list into the three render buckets", () => {
    const hits = [
      mkHit({ id: "c1", type: "controls" }),
      mkHit({ id: "r1", type: "risks" }),
      mkHit({ id: "e1", type: "evidence" }),
      mkHit({ id: "c2", type: "controls" }),
    ];
    const out = groupByType(hits);
    expect(out.controls.map((h) => h.id)).toEqual(["c1", "c2"]);
    expect(out.risks.map((h) => h.id)).toEqual(["r1"]);
    expect(out.evidence.map((h) => h.id)).toEqual(["e1"]);
  });

  test("preserves input order within each bucket", () => {
    const hits = [
      mkHit({ id: "c1", type: "controls" }),
      mkHit({ id: "c2", type: "controls" }),
      mkHit({ id: "c3", type: "controls" }),
    ];
    expect(groupByType(hits).controls.map((h) => h.id)).toEqual([
      "c1",
      "c2",
      "c3",
    ]);
  });
});

describe("hrefForHit", () => {
  test("controls hit routes to per-id detail page", () => {
    expect(hrefForHit(mkHit({ id: "abc-123", type: "controls" }))).toBe(
      "/controls/abc-123",
    );
  });

  test("risks hit routes to hierarchy?focus=<id> (no detail page yet)", () => {
    expect(hrefForHit(mkHit({ id: "r1", type: "risks" }))).toBe(
      "/risks/hierarchy?focus=r1",
    );
  });

  test("evidence hit routes to the list page (no detail page yet)", () => {
    expect(hrefForHit(mkHit({ id: "e1", type: "evidence" }))).toBe(
      "/evidence",
    );
  });

  test("encodes special characters in the id (controls)", () => {
    expect(hrefForHit(mkHit({ id: "AC L-01", type: "controls" }))).toBe(
      "/controls/AC%20L-01",
    );
  });

  test("encodes special characters in the id (risks)", () => {
    expect(hrefForHit(mkHit({ id: "risk one", type: "risks" }))).toBe(
      "/risks/hierarchy?focus=risk%20one",
    );
  });
});

describe("isShortcutTrigger", () => {
  test("matches metaKey+K (mac)", () => {
    expect(isShortcutTrigger({ key: "k", metaKey: true, ctrlKey: false })).toBe(
      true,
    );
  });

  test("matches ctrlKey+K (non-mac)", () => {
    expect(isShortcutTrigger({ key: "k", metaKey: false, ctrlKey: true })).toBe(
      true,
    );
  });

  test("case-insensitive on the K key", () => {
    expect(isShortcutTrigger({ key: "K", metaKey: true, ctrlKey: false })).toBe(
      true,
    );
  });

  test("rejects plain K without modifier", () => {
    expect(
      isShortcutTrigger({ key: "k", metaKey: false, ctrlKey: false }),
    ).toBe(false);
  });

  test("rejects metaKey+other-letter", () => {
    expect(isShortcutTrigger({ key: "j", metaKey: true, ctrlKey: false })).toBe(
      false,
    );
  });

  test("rejects metaKey alone (no key)", () => {
    expect(isShortcutTrigger({ key: "", metaKey: true, ctrlKey: false })).toBe(
      false,
    );
  });
});
