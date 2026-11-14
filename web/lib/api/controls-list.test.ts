// Issue #1832 — the sidebar badge and the /controls page count active
// controls from one source.
//
// The badge (`fetchMyControlsTotal`) and the page (`fetchMyControls`)
// both read `total` off `/api/controls-list?include=state` through
// `activeControlsTotal`. The fake BFF below pages a tenant the way the
// platform does; if either side moves to a different endpoint or a
// different count, the badge and page numbers stop agreeing here.

import { afterEach, describe, expect, test, vi } from "vitest";

import { applyFilters } from "@/app/(authed)/controls/filters";
import { rowsFromAnchors } from "@/app/(authed)/controls/view";

import {
  activeControlsTotal,
  ANCHORS_MAX_PAGES,
  ANCHORS_PAGE_LIMIT,
  fetchCatalogCounts,
  fetchControlsCatalog,
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
      // Parse rather than string-match, so the assertion does not depend
      // on the order the client appends query parameters in.
      const urls = seen.map((u) => new URL(u, "http://localhost"));
      expect(urls[0].searchParams.get("limit")).toBe("1");
      for (const u of urls) {
        expect(u.pathname).toBe("/api/controls-list");
        expect(u.searchParams.get("include")).toBe("state");
      }
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

// Issue #1740 — the SCF catalog view walks every anchor page. A fake
// `/api/controls` pages a 1,200-anchor catalog the way the platform does;
// the last 200 anchors sit in a family only the third page carries.
function fakeAnchors(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    id: `a1740000-0000-4000-8000-${String(i).padStart(12, "0")}`,
    scf_id: `TST-${String(i).padStart(4, "0")}`,
    family: i >= 1000 ? "Late" : "Early",
    name: `Anchor ${i}`,
    description: "",
    state: null,
    frameworks: [],
  }));
}

function stubCatalogBFF(catalogSize: number, reportedTotal = catalogSize) {
  const all = fakeAnchors(catalogSize);
  const seen: URL[] = [];
  const spy = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input) => {
      const url = new URL(String(input), "http://test.local");
      seen.push(url);
      if (url.pathname !== "/api/controls") {
        return new Response(JSON.stringify({ error: "unexpected" }), {
          status: 500,
        });
      }
      const limit = Number(url.searchParams.get("limit") ?? "100");
      const offset = Number(url.searchParams.get("offset") ?? "0");
      const page = all.slice(offset, offset + limit);
      return new Response(
        JSON.stringify({
          anchors: page,
          total: reportedTotal,
          catalog_total: reportedTotal,
          with_control_total: 0,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    });
  return { spy, seen };
}

describe("fetchControlsCatalog (issue 1740)", () => {
  test("walks past the first page, so the family filter sees every anchor", async () => {
    const { seen } = stubCatalogBFF(1200);
    const got = await fetchControlsCatalog();

    expect(got.anchors).toHaveLength(1200);
    expect(got.catalog_total).toBe(1200);
    expect(seen.map((u) => u.searchParams.get("offset"))).toEqual([
      "0",
      "500",
      "1000",
    ]);
    expect(
      seen.every(
        (u) => u.searchParams.get("limit") === String(ANCHORS_PAGE_LIMIT),
      ),
    ).toBe(true);

    const late = applyFilters(rowsFromAnchors(got.anchors), {
      framework: "all",
      family: "Late",
      result: "all",
      freshness: "all",
      scope: "all",
      anchor_fit: "all",
    });
    expect(late).toHaveLength(200);
  });

  test("stops on a full last page when the total is reached", async () => {
    const { seen } = stubCatalogBFF(1000);
    const got = await fetchControlsCatalog();
    expect(got.anchors).toHaveLength(1000);
    expect(seen).toHaveLength(2);
  });

  test("forwards scope and has_control on every page", async () => {
    const { seen } = stubCatalogBFF(600);
    await fetchControlsCatalog("cell-1740", false);
    expect(seen).toHaveLength(2);
    for (const u of seen) {
      expect(u.searchParams.get("scope")).toBe("cell-1740");
      expect(u.searchParams.get("has_control")).toBe("false");
    }
  });

  test("caps the walk when the total is out of reach", async () => {
    // Every page is full and the reported total is past the page cap.
    const { seen } = stubCatalogBFF(12_000);
    const got = await fetchControlsCatalog();
    expect(seen).toHaveLength(ANCHORS_MAX_PAGES);
    expect(got.anchors).toHaveLength(ANCHORS_MAX_PAGES * ANCHORS_PAGE_LIMIT);
    expect(got.total).toBe(12_000);
  });

  // Issue #1895 — a row inserted ahead of the cursor between two page
  // reads shifts the next page by one, so its first anchor repeats the
  // last anchor of the page before.
  test("keeps one copy of an anchor that two pages both carry", async () => {
    const all = fakeAnchors(700);
    const seen: URL[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = new URL(String(input), "http://test.local");
      seen.push(url);
      const offset = Number(url.searchParams.get("offset") ?? "0");
      const start = offset === 0 ? 0 : offset - 1;
      const page = all.slice(start, start + ANCHORS_PAGE_LIMIT);
      return new Response(JSON.stringify({ anchors: page, total: 701 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    const got = await fetchControlsCatalog();
    expect(seen.map((u) => u.searchParams.get("offset"))).toEqual(["0", "500"]);
    const ids = got.anchors.map((a) => a.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids).toHaveLength(700);
  });

  test("throws when a page fails", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "boom" }), { status: 502 }),
    );
    await expect(fetchControlsCatalog()).rejects.toThrow("boom");
  });
});
