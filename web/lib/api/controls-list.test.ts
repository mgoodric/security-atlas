// Issue #1832 — the sidebar badge and the /controls page count active
// controls from one source.
//
// The badge (`fetchMyControlsTotal`) and the page (`fetchMyControls`)
// both read `total` off `/api/controls-list?include=state` through
// `activeControlsTotal`. The fake BFF below pages a tenant the way the
// platform does; if either side moves to a different endpoint or a
// different count, the badge and page numbers stop agreeing here.

import { afterEach, describe, expect, test, vi } from "vitest";

import {
  activeControlsTotal,
  fetchCatalogCounts,
  fetchMyControls,
  fetchMyControlsTotal,
} from "./controls-list";

// Neutral test strings only (P0-A9 / GitGuardian).
function fakeControls(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    id: `c1832000-0000-4000-8000-${String(i).padStart(12, "0")}`,
    title: `Control ${i}`,
    control_family: "Test",
    scf_id: "",
    lifecycle_state: "active",
    bundle_id: `ctrl-1832-${i}`,
    scf_anchor_id: null,
    state: null,
    frameworks: [],
  }));
}

// A fake `/api/controls-list?include=state` that pages like the platform.
// Any other URL is a failure: both counts must come from this one route.
function stubControlsListBFF(tenantSize: number) {
  const all = fakeControls(tenantSize);
  const seen: string[] = [];
  const spy = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input) => {
      const url = new URL(String(input), "http://test.local");
      seen.push(url.pathname + url.search);
      if (
        url.pathname !== "/api/controls-list" ||
        url.searchParams.get("include") !== "state"
      ) {
        return new Response(JSON.stringify({ error: "unexpected" }), {
          status: 500,
        });
      }
      const limit = Number(url.searchParams.get("limit") ?? "100");
      const offset = Number(url.searchParams.get("offset") ?? "0");
      const page = all.slice(offset, offset + limit);
      return new Response(
        JSON.stringify({
          controls: page,
          count: page.length,
          total: all.length,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    });
  return { spy, seen };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("active control count (issue 1832)", () => {
  test.each([0, 1, 39, 500, 1234])(
    "badge total equals the page's total and row count for %i controls",
    async (n) => {
      const { seen } = stubControlsListBFF(n);
      const badge = await fetchMyControlsTotal();
      const page = await fetchMyControls();
      expect(badge).toBe(n);
      expect(page.total).toBe(badge);
      expect(page.controls).toHaveLength(badge);
      expect(seen[0]).toContain("limit=1");
    },
  );

  test("activeControlsTotal prefers total, then count, then the rows", () => {
    expect(activeControlsTotal({ total: 7, count: 2, controls: [] })).toBe(7);
    expect(activeControlsTotal({ count: 2, controls: [] })).toBe(2);
    expect(activeControlsTotal({ controls: fakeControls(3) })).toBe(3);
    expect(activeControlsTotal({})).toBe(0);
  });

  test("the badge surfaces a BFF error rather than a zero", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "boom" }), { status: 502 }),
    );
    await expect(fetchMyControlsTotal()).rejects.toThrow("boom");
  });
});

describe("fetchCatalogCounts (issue 1832)", () => {
  test("asks the catalog BFF for a one-row page", async () => {
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          anchors: [],
          total: 39,
          catalog_total: 1451,
          with_control_total: 39,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    const got = await fetchCatalogCounts();
    const url = new URL(String(spy.mock.calls[0]?.[0]), "http://test.local");
    expect(url.pathname).toBe("/api/controls");
    expect(url.searchParams.get("limit")).toBe("1");
    expect(url.searchParams.get("has_control")).toBe("true");
    expect(got.catalog_total).toBe(1451);
  });
});
