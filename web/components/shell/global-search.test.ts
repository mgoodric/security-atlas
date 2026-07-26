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
  buildSearchRequestURL,
  GROUP_ORDER,
  groupByType,
  hrefForHit,
  isShortcutTrigger,
  LISTBOX_ID,
  optionIdFor,
  resultCountAnnouncement,
  SEARCH_LIMIT,
  SEARCH_MAX_LIMIT,
  SEARCH_MIN_QUERY_LEN,
} from "./global-search";

interface Hit {
  id: string;
  type: "anchors" | "controls" | "risks" | "evidence";
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
  test("partitions an empty list to four empty buckets", () => {
    expect(groupByType([])).toEqual({
      anchors: [],
      controls: [],
      risks: [],
      evidence: [],
    });
  });

  test("partitions a mixed-type list into the four render buckets", () => {
    const hits = [
      mkHit({ id: "c1", type: "controls" }),
      mkHit({ id: "r1", type: "risks" }),
      mkHit({ id: "e1", type: "evidence" }),
      mkHit({ id: "c2", type: "controls" }),
      mkHit({ id: "a1", type: "anchors" }),
    ];
    const out = groupByType(hits);
    expect(out.anchors.map((h) => h.id)).toEqual(["a1"]);
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
  test("anchors hit routes to the SCF catalog detail page (slice 661)", () => {
    expect(hrefForHit(mkHit({ id: "anchor-uuid-1", type: "anchors" }))).toBe(
      "/catalog/scf/anchor-uuid-1",
    );
  });

  test("encodes special characters in the id (anchors)", () => {
    expect(hrefForHit(mkHit({ id: "CRY 04", type: "anchors" }))).toBe(
      "/catalog/scf/CRY%2004",
    );
  });

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
    expect(hrefForHit(mkHit({ id: "e1", type: "evidence" }))).toBe("/evidence");
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

// Slice 361 — WCAG 4.1.2 Name/Role/Value combobox wiring helpers.
// `optionIdFor` and `resultCountAnnouncement` are exported so the pure
// logic can be regression-pinned without standing up the full
// component.
describe("optionIdFor (slice 361)", () => {
  test("anchors row id encodes the type prefix + the upstream id (slice 661)", () => {
    expect(optionIdFor("anchors", "cry-04-uuid")).toBe(
      "global-search-option-anchors-cry-04-uuid",
    );
  });

  test("controls row id encodes the type prefix + the upstream id", () => {
    expect(optionIdFor("controls", "cc-1-2-3")).toBe(
      "global-search-option-controls-cc-1-2-3",
    );
  });

  test("risks row id encodes the type prefix + the upstream id", () => {
    expect(optionIdFor("risks", "r-007")).toBe(
      "global-search-option-risks-r-007",
    );
  });

  test("evidence row id encodes the type prefix + the upstream id", () => {
    expect(optionIdFor("evidence", "ev-42")).toBe(
      "global-search-option-evidence-ev-42",
    );
  });

  test("type-prefix isolates collisions across the four render buckets", () => {
    // Rows with the same id but different types must resolve to
    // distinct DOM ids (the input's `aria-activedescendant` must name
    // exactly one row). Slice 661 adds the `anchors` bucket.
    const anchorsId = optionIdFor("anchors", "shared");
    const controlsId = optionIdFor("controls", "shared");
    const risksId = optionIdFor("risks", "shared");
    const evidenceId = optionIdFor("evidence", "shared");
    expect(new Set([anchorsId, controlsId, risksId, evidenceId]).size).toBe(4);
  });
});

describe("resultCountAnnouncement (slice 361)", () => {
  test("zero results announces 'No results'", () => {
    expect(resultCountAnnouncement(0)).toBe("No results");
  });

  test("one result uses singular form (SR voice naturalness)", () => {
    expect(resultCountAnnouncement(1)).toBe("1 result");
  });

  test("two results uses plural form", () => {
    expect(resultCountAnnouncement(2)).toBe("2 results");
  });

  test("larger counts use plural form", () => {
    expect(resultCountAnnouncement(45)).toBe("45 results");
  });
});

describe("LISTBOX_ID (slice 361)", () => {
  test("is the stable id the input's aria-controls resolves to", () => {
    // Constant by design — pinned so a future rename surfaces a
    // failing test rather than a silent divergence between the input
    // and the popover.
    expect(LISTBOX_ID).toBe("global-search-listbox");
  });
});

// Slice 398b (OE-466) — the request this surface sends must stay inside
// the `GET /v1/search` contract slice 398a (OE-465) pinned in
// docs/openapi.yaml. These pin the client half of that contract; the
// BFF half is pinned in web/app/api/search/route.test.ts.
describe("search request contract (slice 398b)", () => {
  test("SEARCH_MAX_LIMIT mirrors the contract's hard ceiling", () => {
    // docs/openapi.yaml → get-v1-search → limit.maximum, and upstream
    // search.MaxLimit. A value above it is a 400, not a clamp.
    expect(SEARCH_MAX_LIMIT).toBe(50);
  });

  test("SEARCH_MIN_QUERY_LEN mirrors the contract's q.minLength", () => {
    expect(SEARCH_MIN_QUERY_LEN).toBe(2);
  });

  test("SEARCH_LIMIT never exceeds the contract's hard ceiling", () => {
    // The regression this guards: raising PER_TYPE_TARGET or adding a
    // fifth group pushes the derived limit past 50, and every search
    // starts answering 400 instead of returning hits.
    expect(SEARCH_LIMIT).toBeLessThanOrEqual(SEARCH_MAX_LIMIT);
    expect(SEARCH_LIMIT).toBeGreaterThan(0);
  });

  test("SEARCH_LIMIT covers every rendered group", () => {
    // The bug this pins: slice 661 added the fourth (`anchors`) group
    // but left the multiplier at three, so the surface asked for 36
    // merged hits while its own comment claimed 48 — the last group
    // could be starved by the upstream's cross-type relevance sort.
    // Deriving the limit from GROUP_ORDER makes that drift impossible.
    expect(SEARCH_LIMIT).toBeGreaterThanOrEqual(GROUP_ORDER.length);
    expect(SEARCH_LIMIT % GROUP_ORDER.length).toBe(0);
  });

  test("builds a BFF URL carrying q and the derived limit", () => {
    expect(buildSearchRequestURL("iam")).toBe(
      `/api/search?q=iam&limit=${SEARCH_LIMIT}`,
    );
  });

  test("trims the raw input before sending it", () => {
    expect(buildSearchRequestURL("  encryption  ")).toBe(
      `/api/search?q=encryption&limit=${SEARCH_LIMIT}`,
    );
  });

  test("URL-encodes the free-text query (P0-272-3)", () => {
    // The query is untrusted free text. It must ride as an encoded
    // query-string VALUE — never as a path segment, and never able to
    // inject an extra parameter of its own.
    const url = buildSearchRequestURL("a&limit=999#x /../v1/tenants");
    expect(url).toBe(
      `/api/search?q=a%26limit%3D999%23x%20%2F..%2Fv1%2Ftenants&limit=${SEARCH_LIMIT}`,
    );
    expect(url.startsWith("/api/search?q=")).toBe(true);
    // Exactly two parameters — the injected `limit=999` stayed inside
    // the encoded value rather than becoming a parameter.
    expect(url.split("&")).toHaveLength(2);
  });

  test("omits `types` so a new upstream type reaches the UI unchanged", () => {
    // The contract defines "omit for all supported types". Sending an
    // explicit list would silently exclude any type added upstream
    // later — the failure mode that hid SCF anchors before slice 661.
    expect(buildSearchRequestURL("iam")).not.toContain("types=");
  });
});
