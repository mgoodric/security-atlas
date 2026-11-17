import { describe, expect, test } from "vitest";

import { searchFromView, viewFromSearch } from "./saved-views";

const OWNER = "6f1b1a3e-1c3b-4a2b-9a1e-1d2c3b4a5f60";

describe("risk register saved views (#2133)", () => {
  test("a view holds the filters, sort, roll-ups and columns, defaults left out", () => {
    const search = new URLSearchParams(
      `status=all&owner=${OWNER}&treatment=mitigate&category=all&sort=-title&rollups=1&cols=themes,methodology&page=3&archived=1`,
    );
    expect(viewFromSearch(search)).toEqual({
      status: "all",
      owner: OWNER,
      treatment: "mitigate",
      sort: "-title",
      include_rollups: "true",
      cols: "methodology,themes",
    });
  });

  test("an unfiltered register saves an empty view", () => {
    expect(viewFromSearch(new URLSearchParams(""))).toEqual({});
  });

  test("applying a view gives back the URL it was saved from", () => {
    const url =
      "status=closed&owner=none&review_due=overdue&sort=title&rollups=1&cols=level,created_at";
    const view = viewFromSearch(new URLSearchParams(url));
    const applied = new URLSearchParams(searchFromView(view));
    expect(viewFromSearch(applied)).toEqual(view);
    expect(applied.get("rollups")).toBe("1");
    expect(applied.get("cols")).toBe("level,created_at");
    expect(applied.get("page")).toBeNull();
  });

  test("an entry the register no longer knows is dropped on apply", () => {
    const qs = searchFromView({
      treatment: "retired",
      owner: "none",
      sort: "colour",
      cols: "gone,themes",
    });
    expect(qs).toBe("owner=none&cols=themes");
  });
});
