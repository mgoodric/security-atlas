// #1748 — the attach picker's level rule. It mirrors the platform: a child
// sits strictly below its parent (team < org < company). A risk with no
// level (an older wire shape) is never offered.

import { afterEach, describe, expect, test, vi } from "vitest";

import {
  canBeChildOf,
  fetchRisksList,
  fetchRisksListWithRollUps,
  listRisks,
} from "./risks";

// #2060 — roll-ups stay out of the register unless a caller asks for them.
describe("risk list fetchers and roll-ups", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function stubFetch(body: unknown, status = 200) {
    return vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response(JSON.stringify(body), { status }));
  }

  test.each([
    [false, "/v1/risks"],
    [true, "/v1/risks?include_rollups=true"],
  ])("listRisks(includeRollups=%s) asks upstream for %s", async (inc, path) => {
    const spy = stubFetch({ risks: [{ id: "r1" }], count: 1 });
    const risks = await listRisks("test-bearer-2060", inc);
    expect(risks).toEqual([{ id: "r1" }]);
    expect(String(spy.mock.calls[0]?.[0]).endsWith(path)).toBe(true);
  });

  test.each([
    [fetchRisksList, "/api/risks"],
    [fetchRisksListWithRollUps, "/api/risks?include_rollups=true"],
  ])("%o reads the BFF at %s", async (fetcher, url) => {
    const spy = stubFetch({ risks: [] });
    await expect(fetcher()).resolves.toEqual({ risks: [] });
    expect(spy.mock.calls[0]?.[0]).toBe(url);
  });

  test("a BFF error surfaces its message", async () => {
    stubFetch({ error: "upstream down" }, 502);
    await expect(fetchRisksListWithRollUps()).rejects.toThrow("upstream down");
  });
});

describe("canBeChildOf", () => {
  test("a lower level can be a child", () => {
    expect(canBeChildOf({ level: "team" }, { level: "org" })).toBe(true);
    expect(canBeChildOf({ level: "team" }, { level: "company" })).toBe(true);
    expect(canBeChildOf({ level: "org" }, { level: "company" })).toBe(true);
  });

  test("the same or a higher level cannot", () => {
    expect(canBeChildOf({ level: "org" }, { level: "org" })).toBe(false);
    expect(canBeChildOf({ level: "company" }, { level: "org" })).toBe(false);
    expect(canBeChildOf({ level: "org" }, { level: "team" })).toBe(false);
  });

  test("a missing level is never offered", () => {
    expect(canBeChildOf({}, { level: "company" })).toBe(false);
    expect(canBeChildOf({ level: "team" }, {})).toBe(false);
  });
});
