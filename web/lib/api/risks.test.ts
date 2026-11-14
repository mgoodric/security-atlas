// #1748 — the attach picker's level rule. It mirrors the platform: a child
// sits strictly below its parent (team < org < company). A risk with no
// level (an older wire shape) is never offered.

import { describe, expect, test } from "vitest";

import { canBeChildOf } from "./risks";

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
