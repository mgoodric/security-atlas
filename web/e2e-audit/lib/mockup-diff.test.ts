// Slice 178 — diff-categorization unit tests (AC-11a / AC-11b / AC-11c).
//
// Pure-function module; no I/O. Every branch of `diffRoute` is covered
// with a fixture input that constructs the exact LiveFingerprint +
// ManifestEntry shape the harness produces.

import { describe, expect, it } from "vitest";

import {
  diffRoute,
  sortFindings,
  summarize,
  type LiveFingerprint,
  type ManifestEntry,
} from "./mockup-diff";

function emptyLive(route: string): LiveFingerprint {
  return {
    route,
    testIds: [],
    deadAnchors: [],
    comingSoonButtons: [],
    unsetFeatureFlags: [],
  };
}

function manifest(
  route: string,
  expected: string[],
  allowed: string[] = [],
  mockupPath: string | null = `${route.slice(1).replace(/\//g, "-")}.html`,
  stale: string[] = [],
): ManifestEntry {
  return {
    route,
    mockupPath,
    expectedTestIds: expected,
    allowedExtraTestIds: allowed,
    staleMockupTestIds: stale,
  };
}

describe("diffRoute — AC-11a: SHIP-GAP", () => {
  it("flags expected testids missing from live", () => {
    const live = emptyLive("/dashboard");
    const m = manifest("/dashboard", ["program-dashboard", "framework-tile"]);
    const findings = diffRoute(live, m);
    expect(findings.filter((f) => f.category === "SHIP-GAP")).toHaveLength(2);
    expect(
      findings.find((f) => f.subject === "program-dashboard"),
    ).toBeTruthy();
  });

  it("does not flag SHIP-GAP for routes with mockupPath=null", () => {
    const live = emptyLive("/calendar");
    const m = manifest("/calendar", ["calendar-grid"], [], null);
    const findings = diffRoute(live, m);
    expect(findings.filter((f) => f.category === "SHIP-GAP")).toHaveLength(0);
  });

  it("does not flag SHIP-GAP for testids the manifest marks stale", () => {
    const live = emptyLive("/dashboard");
    const m = manifest(
      "/dashboard",
      ["program-dashboard", "framework-tile"],
      [],
      "dashboard.html",
      ["framework-tile"],
    );
    const findings = diffRoute(live, m);
    // SHIP-GAP only for program-dashboard; framework-tile falls into
    // MOCKUP-STALE instead.
    const shipGaps = findings.filter((f) => f.category === "SHIP-GAP");
    expect(shipGaps).toHaveLength(1);
    expect(shipGaps[0].subject).toBe("program-dashboard");
  });
});

describe("diffRoute — AC-11b: HONESTY-GAP", () => {
  it("flags live testids that are neither expected nor allowed", () => {
    const live: LiveFingerprint = {
      ...emptyLive("/dashboard"),
      testIds: ["program-dashboard", "secret-vendor-card"],
    };
    const m = manifest("/dashboard", ["program-dashboard"]);
    const findings = diffRoute(live, m);
    const honestyGaps = findings.filter((f) => f.category === "HONESTY-GAP");
    expect(honestyGaps).toHaveLength(1);
    expect(honestyGaps[0].subject).toBe("secret-vendor-card");
  });

  it("does not flag testids on the allow-list", () => {
    const live: LiveFingerprint = {
      ...emptyLive("/dashboard"),
      testIds: ["program-dashboard", "shipped-post-mockup"],
    };
    const m = manifest(
      "/dashboard",
      ["program-dashboard"],
      ["shipped-post-mockup"],
    );
    const findings = diffRoute(live, m);
    expect(findings.filter((f) => f.category === "HONESTY-GAP")).toHaveLength(
      0,
    );
  });

  it("flags dead anchors (AC-5a)", () => {
    const live: LiveFingerprint = {
      ...emptyLive("/dashboard"),
      deadAnchors: [{ href: "#", text: "Vendors" }],
    };
    const m = manifest("/dashboard", []);
    const findings = diffRoute(live, m);
    const honestyGaps = findings.filter((f) => f.category === "HONESTY-GAP");
    expect(honestyGaps).toHaveLength(1);
    expect(honestyGaps[0].subject).toBe('a[href="#"]');
    expect(honestyGaps[0].details).toContain("Vendors");
  });

  it("flags coming-soon buttons (AC-5b)", () => {
    const live: LiveFingerprint = {
      ...emptyLive("/dashboard"),
      comingSoonButtons: [{ text: "Export PDF", ariaLabel: "coming soon" }],
    };
    const m = manifest("/dashboard", []);
    const findings = diffRoute(live, m);
    const honestyGaps = findings.filter((f) => f.category === "HONESTY-GAP");
    expect(honestyGaps).toHaveLength(1);
    expect(honestyGaps[0].details).toContain("Export PDF");
  });

  it("flags unset feature flags (AC-5c)", () => {
    const live: LiveFingerprint = {
      ...emptyLive("/dashboard"),
      unsetFeatureFlags: [
        { flag: "ai-board-narrative", selector: '[data-testid="ai-panel"]' },
      ],
    };
    const m = manifest("/dashboard", []);
    const findings = diffRoute(live, m);
    const honestyGaps = findings.filter((f) => f.category === "HONESTY-GAP");
    expect(honestyGaps).toHaveLength(1);
    expect(honestyGaps[0].subject).toBe(
      '[data-feature-flag="ai-board-narrative"]',
    );
  });
});

describe("diffRoute — AC-11c: MOCKUP-STALE", () => {
  it("flags every staleMockupTestId", () => {
    const live = emptyLive("/dashboard");
    const m = manifest("/dashboard", [], [], "dashboard.html", [
      "deprecated-trust-center-card",
      "deprecated-magic-link",
    ]);
    const findings = diffRoute(live, m);
    const stale = findings.filter((f) => f.category === "MOCKUP-STALE");
    expect(stale).toHaveLength(2);
    expect(stale.map((f) => f.subject).sort()).toEqual([
      "deprecated-magic-link",
      "deprecated-trust-center-card",
    ]);
  });

  it("does not flag MOCKUP-STALE when mockupPath is null", () => {
    const live = emptyLive("/calendar");
    const m = manifest("/calendar", [], [], null, ["should-be-ignored"]);
    const findings = diffRoute(live, m);
    expect(findings.filter((f) => f.category === "MOCKUP-STALE")).toHaveLength(
      0,
    );
  });
});

describe("sortFindings", () => {
  it("orders by category (HONESTY > SHIP > MOCKUP-STALE), then route, then subject", () => {
    const findings = [
      {
        route: "/b",
        category: "MOCKUP-STALE" as const,
        subject: "x",
        mockupPath: null,
        suggestedAction: "",
      },
      {
        route: "/a",
        category: "HONESTY-GAP" as const,
        subject: "z",
        mockupPath: null,
        suggestedAction: "",
      },
      {
        route: "/a",
        category: "SHIP-GAP" as const,
        subject: "a",
        mockupPath: null,
        suggestedAction: "",
      },
      {
        route: "/a",
        category: "HONESTY-GAP" as const,
        subject: "a",
        mockupPath: null,
        suggestedAction: "",
      },
    ];
    const sorted = sortFindings(findings);
    expect(sorted.map((f) => `${f.category}:${f.route}:${f.subject}`)).toEqual([
      "HONESTY-GAP:/a:a",
      "HONESTY-GAP:/a:z",
      "SHIP-GAP:/a:a",
      "MOCKUP-STALE:/b:x",
    ]);
  });
});

describe("summarize", () => {
  it("aggregates totals + per-route counts", () => {
    const findings = [
      {
        route: "/a",
        category: "HONESTY-GAP" as const,
        subject: "x",
        mockupPath: null,
        suggestedAction: "",
      },
      {
        route: "/a",
        category: "SHIP-GAP" as const,
        subject: "y",
        mockupPath: null,
        suggestedAction: "",
      },
      {
        route: "/b",
        category: "MOCKUP-STALE" as const,
        subject: "z",
        mockupPath: null,
        suggestedAction: "",
      },
    ];
    const s = summarize(findings);
    expect(s.total).toBe(3);
    expect(s.honestyGap).toBe(1);
    expect(s.shipGap).toBe(1);
    expect(s.mockupStale).toBe(1);
    expect(s.byRoute["/a"]).toBe(2);
    expect(s.byRoute["/b"]).toBe(1);
  });
});
