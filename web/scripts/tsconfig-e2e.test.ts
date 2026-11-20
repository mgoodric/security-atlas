// #2605 — tsconfig.e2e.json type-checks every Playwright spec.
//
// The config once carried an exclude list of specs with type errors,
// marked "never add to it" and enforced by nothing. #2605 fixed those
// specs and emptied it; this keeps it empty, so a spec that stops
// type-checking is fixed rather than excluded.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const config = JSON.parse(
  readFileSync(join(__dirname, "..", "tsconfig.e2e.json"), "utf8"),
) as { include: string[]; exclude: string[] };

describe("tsconfig.e2e.json", () => {
  it("includes every spec under e2e/", () => {
    expect(config.include).toContain("e2e/**/*.ts");
  });

  it("excludes no spec", () => {
    expect(config.exclude).toEqual(["node_modules", ".next"]);
  });
});
