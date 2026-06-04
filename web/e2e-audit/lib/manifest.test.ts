// Slice 178 — manifest validator unit tests (AC-10, AC-10a, P0-178-11).
//
// Covers the validateManifest branches without touching the disk (the
// `loadManifest` path is exercised implicitly by the harness at run
// time + by the file-existence check the CI job wires up).

import { describe, expect, it } from "vitest";

import { validateManifest } from "./manifest";

describe("validateManifest", () => {
  it("accepts a minimal valid entry with mockupPath=null", () => {
    const result = validateManifest([
      {
        route: "/calendar",
        mockupPath: null,
        expectedTestIds: [],
        allowedExtraTestIds: [],
      },
    ]);
    expect(result.ok).toBe(true);
    expect(result.entries).toHaveLength(1);
  });

  it("rejects a non-array root", () => {
    const result = validateManifest({ not: "an array" });
    expect(result.ok).toBe(false);
    expect(result.errors[0].message).toMatch(/array/);
  });

  it("rejects a route that does not start with /", () => {
    const result = validateManifest([
      {
        route: "dashboard",
        mockupPath: null,
        expectedTestIds: [],
        allowedExtraTestIds: [],
      },
    ]);
    expect(result.ok).toBe(false);
  });

  it("rejects a mockupPath that fails the regex shape", () => {
    const result = validateManifest([
      {
        route: "/x",
        mockupPath: "Plans/mockups/dashboard.html",
        expectedTestIds: [],
        allowedExtraTestIds: [],
      },
    ]);
    expect(result.ok).toBe(false);
    expect(result.errors.some((e) => /must match/.test(e.message))).toBe(true);
  });

  it("rejects a mockupPath that does not exist on disk (P0-178-11)", () => {
    const result = validateManifest([
      {
        route: "/x",
        mockupPath: "definitely-not-a-real-mockup.html",
        expectedTestIds: [],
        allowedExtraTestIds: [],
      },
    ]);
    expect(result.ok).toBe(false);
    expect(result.errors.some((e) => /does not exist/.test(e.message))).toBe(
      true,
    );
  });

  it("accepts a real mockup path", () => {
    // dashboard.html does exist under Plans/_archive/mockups/. This branch is
    // the inverse of the prior test; together they prove the
    // file-existence check runs.
    const result = validateManifest([
      {
        route: "/dashboard",
        mockupPath: "dashboard.html",
        expectedTestIds: ["program-dashboard"],
        allowedExtraTestIds: [],
      },
    ]);
    expect(result.ok).toBe(true);
  });

  it("rejects duplicate routes", () => {
    const result = validateManifest([
      {
        route: "/x",
        mockupPath: null,
        expectedTestIds: [],
        allowedExtraTestIds: [],
      },
      {
        route: "/x",
        mockupPath: null,
        expectedTestIds: [],
        allowedExtraTestIds: [],
      },
    ]);
    expect(result.ok).toBe(false);
    expect(result.errors.some((e) => /duplicate/.test(e.message))).toBe(true);
  });

  it("rejects non-unique expectedTestIds", () => {
    const result = validateManifest([
      {
        route: "/x",
        mockupPath: null,
        expectedTestIds: ["a", "a"],
        allowedExtraTestIds: [],
      },
    ]);
    expect(result.ok).toBe(false);
    expect(result.errors.some((e) => /unique/.test(e.message))).toBe(true);
  });

  it("validates the production manifest file (smoke)", async () => {
    const { loadManifest } = await import("./manifest");
    // Throws on failure — calling it is the test.
    const entries = loadManifest();
    expect(entries.length).toBeGreaterThan(0);
    // Every entry honors AC-8's locked v1 route set.
    const routes = entries.map((e) => e.route);
    for (const r of [
      "/dashboard",
      "/controls",
      "/controls/:id",
      "/evidence",
      "/audits",
      "/risks",
      "/policies",
      "/board-packs",
      "/settings",
      "/calendar",
    ]) {
      expect(routes).toContain(r);
    }
  });
});
