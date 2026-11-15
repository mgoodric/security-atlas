import { describe, expect, test } from "vitest";

import { formatAddedOn } from "@/components/mappings/format";

import { isOrgMapping } from "./org-mapping";

describe("isOrgMapping", () => {
  test("only the platform's overlay origin marks an org mapping", () => {
    expect(isOrgMapping({ origin: "overlay" })).toBe(true);
    expect(isOrgMapping({ origin: "catalog" })).toBe(false);
    expect(isOrgMapping({})).toBe(false);
  });

  test("source_attribution alone never marks one", () => {
    const row = { origin: "catalog", source_attribution: "org_internal" };
    expect(isOrgMapping(row)).toBe(false);
  });
});

describe("formatAddedOn", () => {
  test("renders the UTC date", () => {
    expect(formatAddedOn("2026-11-01T23:30:00-05:00")).toBe("2026-11-02");
  });

  test("passes an unparseable value through", () => {
    expect(formatAddedOn("not a date")).toBe("not a date");
  });
});
