// Slice 380 -- vitest coverage for dashboard-prefetch.ts.
//
// Two load-bearing properties are pinned here:
//   1. Key parity (AC-6, drift guard): the prefetch query keys MUST be
//      byte-identical to the keys the page's six `useQuery` hooks
//      register under. A drift silently disables the hydration prime
//      and the client falls back to the cold BFF fetch (re-introducing
//      slice 332 F-BFF-2). The page hard-codes its keys inline; this
//      test is the cross-module binding.
//   2. Fail-soft fan-out (D3): `prefetchDashboard` must (a) seed a
//      QueryClient on success with the SAME shape the client useQuery
//      expects, (b) skip a panel whose upstream throws WITHOUT seeding
//      an error state and WITHOUT rejecting the whole Promise.all, and
//      (c) no-op on a missing bearer.

import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  DASHBOARD_PANEL_PREFETCHES,
  DASHBOARD_QUERY_KEYS,
  E2E_NO_PREFETCH_COOKIE,
  prefetchDashboard,
  serverPrefetchBypassed,
} from "./dashboard-prefetch";

// Mock the typed client module. `dashboard-prefetch.ts` imports the six
// upstream fns as named ESM bindings; a `vi.mock` factory is the
// reliable way to intercept those bindings (a `vi.spyOn` on a namespace
// import is not consistently rebound under the ESM live-binding
// semantics). Each fn is a `vi.fn()` the tests program per-case.
vi.mock("@/lib/api/dashboard", () => ({
  getControlDrift: vi.fn(),
  getEvidenceFreshness: vi.fn(),
  getMitigateRisks: vi.fn(),
  getUpcoming: vi.fn(),
  getFrameworkPosture: vi.fn(),
  getActivity: vi.fn(),
}));

// Import the mocked module AFTER vi.mock so `api.*` are the vi.fn()s.
import * as api from "@/lib/api/dashboard";

const mocked = api as unknown as Record<string, ReturnType<typeof vi.fn>>;

beforeEach(() => {
  // Default every fn to resolve a benign empty object so a test that
  // only programs one fn still has the other five succeed.
  for (const key of [
    "getControlDrift",
    "getEvidenceFreshness",
    "getMitigateRisks",
    "getUpcoming",
    "getFrameworkPosture",
    "getActivity",
  ]) {
    mocked[key].mockReset();
    mocked[key].mockResolvedValue({});
  }
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("DASHBOARD_QUERY_KEYS (key parity with page.tsx useQuery)", () => {
  // The page (web/app/(authed)/dashboard/page.tsx) registers exactly
  // these keys (lines ~54-77). If the page renames a key, this test
  // fails -- much better than silently re-introducing the cold-fetch
  // regression.
  it("drift key matches the page useQuery key", () => {
    expect(DASHBOARD_QUERY_KEYS.drift).toEqual(["dashboard", "drift"]);
  });
  it("freshness key matches the page useQuery key", () => {
    expect(DASHBOARD_QUERY_KEYS.freshness).toEqual(["dashboard", "freshness"]);
  });
  it("risks key matches the page useQuery key", () => {
    expect(DASHBOARD_QUERY_KEYS.risks).toEqual(["dashboard", "risks"]);
  });
  it("upcoming key matches the page useQuery key", () => {
    expect(DASHBOARD_QUERY_KEYS.upcoming).toEqual(["dashboard", "upcoming"]);
  });
  it("framework-posture key matches the page useQuery key", () => {
    expect(DASHBOARD_QUERY_KEYS.frameworkPosture).toEqual([
      "dashboard",
      "framework-posture",
    ]);
  });
  it("activity key matches the page useQuery key", () => {
    expect(DASHBOARD_QUERY_KEYS.activity).toEqual(["dashboard", "activity"]);
  });
});

describe("DASHBOARD_PANEL_PREFETCHES (fan-out enumeration)", () => {
  it("enumerates all six panels exactly once", () => {
    expect(DASHBOARD_PANEL_PREFETCHES).toHaveLength(6);
    const keys = DASHBOARD_PANEL_PREFETCHES.map((p) => p.queryKey.join("/"));
    expect(new Set(keys).size).toBe(6);
  });
});

describe("prefetchDashboard (parallel fan-out, fail-soft)", () => {
  it("seeds every panel's cache on success under the page's keys (AC-6)", async () => {
    // Stub each upstream fn to a deterministic shape; assert the
    // dehydrated cache carries it under the page's queryKey.
    const posture = { frameworks: [], count: 0 };
    const risks = [{ id: "r1" }];
    const freshness = {
      bucket: "class",
      buckets: [],
      total: 0,
      total_stale: 0,
    };
    const drift = { snapshots: [] };
    const upcoming = { items: [], count: 0, next_cursor: "" };
    const activity = { activity: [], count: 0, next_cursor: "" };

    mocked.getFrameworkPosture.mockResolvedValue(posture);
    mocked.getMitigateRisks.mockResolvedValue(risks);
    mocked.getEvidenceFreshness.mockResolvedValue(freshness);
    mocked.getControlDrift.mockResolvedValue(drift);
    mocked.getUpcoming.mockResolvedValue(upcoming);
    mocked.getActivity.mockResolvedValue(activity);

    const qc = new QueryClient();
    await prefetchDashboard(qc, "bearer-token");

    expect(qc.getQueryData(["dashboard", "framework-posture"])).toEqual(
      posture,
    );
    expect(qc.getQueryData(["dashboard", "risks"])).toEqual(risks);
    expect(qc.getQueryData(["dashboard", "freshness"])).toEqual(freshness);
    expect(qc.getQueryData(["dashboard", "drift"])).toEqual(drift);
    expect(qc.getQueryData(["dashboard", "upcoming"])).toEqual(upcoming);
    expect(qc.getQueryData(["dashboard", "activity"])).toEqual(activity);
  });

  it("forwards the bearer to each upstream fn (P0-3)", async () => {
    const qc = new QueryClient();
    await prefetchDashboard(qc, "the-bearer");
    expect(mocked.getFrameworkPosture).toHaveBeenCalledWith("the-bearer");
    expect(mocked.getMitigateRisks).toHaveBeenCalledWith("the-bearer");
    expect(mocked.getEvidenceFreshness).toHaveBeenCalledWith("the-bearer");
    // getControlDrift is called with one arg (the bearer); its `since`
    // param defaults to "7d" inside the fn, matching the client path.
    expect(mocked.getControlDrift).toHaveBeenCalledWith("the-bearer");
    expect(mocked.getUpcoming).toHaveBeenCalledWith("the-bearer");
    expect(mocked.getActivity).toHaveBeenCalledWith("the-bearer");
  });

  it("fails soft on a single upstream error without rejecting (D3)", async () => {
    // The drift upstream throws; the other five still seed, and the
    // failing panel is left UNseeded (no error-state in the cache).
    mocked.getFrameworkPosture.mockResolvedValue({ ok: 1 });
    mocked.getMitigateRisks.mockResolvedValue([]);
    mocked.getEvidenceFreshness.mockResolvedValue({ ok: 1 });
    mocked.getControlDrift.mockRejectedValue(new Error("upstream 500"));
    mocked.getUpcoming.mockResolvedValue({ ok: 1 });
    mocked.getActivity.mockResolvedValue({ ok: 1 });

    const qc = new QueryClient();
    // Must NOT reject:
    await expect(prefetchDashboard(qc, "bearer")).resolves.toBeUndefined();

    // The five healthy panels are seeded:
    expect(qc.getQueryData(["dashboard", "framework-posture"])).toEqual({
      ok: 1,
    });
    expect(qc.getQueryData(["dashboard", "upcoming"])).toEqual({ ok: 1 });

    // The failed panel is UNseeded (undefined) -- NOT an error state.
    // This is the load-bearing fail-soft property: the client useQuery
    // re-fetches it cold rather than hydrating into an error.
    expect(qc.getQueryData(["dashboard", "drift"])).toBeUndefined();
    const driftState = qc.getQueryState(["dashboard", "drift"]);
    expect(driftState?.status).not.toBe("error");
  });

  it("no-ops on a missing bearer without seeding or calling upstream (P0-3)", async () => {
    const qc = new QueryClient();
    await expect(prefetchDashboard(qc, undefined)).resolves.toBeUndefined();

    expect(mocked.getFrameworkPosture).not.toHaveBeenCalled();
    expect(qc.getQueryData(["dashboard", "framework-posture"])).toBeUndefined();
  });
});

describe("E2E_NO_PREFETCH_COOKIE", () => {
  it("is the test-only bypass cookie name the layout reads", () => {
    expect(E2E_NO_PREFETCH_COOKIE).toBe("e2e_no_prefetch");
  });
});

describe("serverPrefetchBypassed (test-mode gate, D6)", () => {
  const original = process.env.ATLAS_TEST_MODE;

  afterEach(() => {
    if (original === undefined) {
      delete process.env.ATLAS_TEST_MODE;
    } else {
      process.env.ATLAS_TEST_MODE = original;
    }
  });

  it("is true only when ATLAS_TEST_MODE === '1'", () => {
    process.env.ATLAS_TEST_MODE = "1";
    expect(serverPrefetchBypassed()).toBe(true);
  });

  it("is false when ATLAS_TEST_MODE is unset (production posture)", () => {
    delete process.env.ATLAS_TEST_MODE;
    expect(serverPrefetchBypassed()).toBe(false);
  });

  it("is false for any value other than the literal '1'", () => {
    // A truthy-but-not-"1" value must NOT enable the bypass -- the gate
    // is an exact-match check, mirroring the Go-side ATLAS_TEST_MODE
    // convention (internal/api/testissuejwt.go).
    process.env.ATLAS_TEST_MODE = "true";
    expect(serverPrefetchBypassed()).toBe(false);
    process.env.ATLAS_TEST_MODE = "0";
    expect(serverPrefetchBypassed()).toBe(false);
  });
});
