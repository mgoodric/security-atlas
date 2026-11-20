// #2711 — repo-wide sentinel: every Playwright test asserts something.
//
// A `test(...)` whose body holds no `expect` call passes while testing
// nothing, and inflates the e2e green count. board-pack-detail.spec.ts and
// control-detail-empty.spec.ts did exactly that: their assertions sat in
// comments for months behind the slice-082 quarantine.
//
// The scan parses each spec under `e2e/` and `e2e-audit/` with the
// TypeScript compiler (comments are not part of the tree, so a commented
// assertion does not count) and flags a `test`, `test.only` or `test.fail`
// call whose callback contains no call to `expect`, `expect.<modifier>` or
// a helper named `expect...` (the suite's convention for assertion
// helpers, e.g. `expectNoRawIds`). `test.skip` and `test.fixme` report as
// skipped, not passed, so they are not flagged.
//
// KNOWN_EMPTY is the ratchet: specs that already had such tests when the
// guard landed, with their count. A count may only go down — lower it in
// the PR that restores a spec's assertions, and drop the entry at zero.
// The test fails on a count that rises, a new file, and a stale entry.

import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import ts from "typescript";
import { describe, expect, test } from "vitest";

const WEB_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const SPEC_DIRS = ["e2e", "e2e-audit"];

const KNOWN_EMPTY: Record<string, number> = {
  "e2e/admin-bootstrap.spec.ts": 4,
  "e2e/audit-periods-vendors-export.spec.ts": 7,
  "e2e/audits-list.spec.ts": 12,
  "e2e/calendar.spec.ts": 8,
  "e2e/control-detail.spec.ts": 13,
  "e2e/controls-list.spec.ts": 13,
  "e2e/evidence-list.spec.ts": 14,
  "e2e/evidence-pagination.spec.ts": 7,
  "e2e/exceptions-list.spec.ts": 12,
  "e2e/metrics-dashboard.spec.ts": 10,
  "e2e/policies-list.spec.ts": 15,
  "e2e/risk-hierarchy.spec.ts": 8,
  "e2e/risks-list.spec.ts": 20,
};

const COUNTED_TEST_CALL = /^test(\.(only|fail))?$/;
const ASSERTION_CALL = /^expect/;

/** Titles of the counted tests in `source` whose body never calls expect. */
function testsWithoutExpect(fileName: string, source: string): string[] {
  const sf = ts.createSourceFile(
    fileName,
    source,
    ts.ScriptTarget.Latest,
    true,
  );
  const hits: string[] = [];
  const hasAssertion = (node: ts.Node): boolean => {
    if (
      ts.isCallExpression(node) &&
      ASSERTION_CALL.test(node.expression.getText(sf))
    ) {
      return true;
    }
    return ts.forEachChild(node, hasAssertion) ?? false;
  };
  const visit = (node: ts.Node): void => {
    if (
      ts.isCallExpression(node) &&
      COUNTED_TEST_CALL.test(node.expression.getText(sf))
    ) {
      const [title] = node.arguments;
      const body = node.arguments.find(
        (a) => ts.isArrowFunction(a) || ts.isFunctionExpression(a),
      );
      if (
        title &&
        ts.isStringLiteralLike(title) &&
        body &&
        !hasAssertion(body)
      ) {
        hits.push(title.text);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return hits;
}

function emptyTestCounts(): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const dir of SPEC_DIRS) {
    for (const entry of readdirSync(join(WEB_ROOT, dir))) {
      if (!entry.endsWith(".spec.ts")) continue;
      const rel = `${dir}/${entry}`;
      const n = testsWithoutExpect(
        rel,
        readFileSync(join(WEB_ROOT, rel), "utf8"),
      ).length;
      if (n > 0) counts[rel] = n;
    }
  }
  return counts;
}

describe("every Playwright test asserts something (#2711)", () => {
  test("no spec gains a test without an expect call; KNOWN_EMPTY only shrinks", () => {
    expect(emptyTestCounts()).toEqual(KNOWN_EMPTY);
  });

  test("the scan flags a body whose assertions are commented out", () => {
    const src = `
      import { test, expect } from "./fixtures";
      test("commented", async ({ page }) => {
        // await expect(page).toHaveURL(/x/);
      });
      test.only("empty", () => {});
      test.fail("no assert", async ({ page }) => { await page.goto("/"); });
    `;
    expect(testsWithoutExpect("x.spec.ts", src)).toEqual([
      "commented",
      "empty",
      "no assert",
    ]);
  });

  test("the scan passes expect, expect modifiers and expect helpers", () => {
    const src = `
      test("plain", async ({ page }) => { await expect(page).toHaveURL(/x/); });
      test("soft", async ({ page }) => { await expect.soft(page).toHaveURL(/x/); });
      test("poll", async () => { await expect.poll(() => 1).toBe(1); });
      test("helper", async ({ page }) => { await expectNoRawIds(page); });
      test("nested", async ({ page }) => {
        await test.step("s", async () => { expect(1).toBe(1); });
      });
      test.skip("skipped", () => {});
      test.fixme("fixme", () => {});
      test.describe("group", () => {});
      test.beforeEach(async ({ page }) => { await page.goto("/"); });
    `;
    expect(testsWithoutExpect("x.spec.ts", src)).toEqual([]);
  });
});
