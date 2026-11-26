// #2711 — repo-wide sentinel: every Playwright test asserts something.
//
// A `test(...)` whose body holds no `expect` call passes while testing
// nothing, and inflates the e2e green count. board-pack-detail.spec.ts and
// control-detail-empty.spec.ts did exactly that: their assertions sat in
// comments for months behind the slice-082 quarantine.
//
// The scan parses each spec under `e2e/` and `e2e-audit/`, subdirectories
// included, with the TypeScript compiler (comments are not part of the
// tree, so a commented assertion does not count) and flags a `test`,
// `test.only` or `test.fail` call whose callback contains no call to
// `expect`, `expect.<modifier>` or a helper named `expect...` (the suite's
// convention for assertion helpers, e.g. `expectNoRawIds`). A call to a
// function declared in the same file counts when that function's body
// asserts. Aliases count as `test`: `import { test as authed }` and
// `const x = base.extend(...)`. Titles need not be literals, so a test
// built in a loop from a template title is checked too. `test.skip` and
// `test.fixme` report as skipped, not passed, so they are not flagged.
//
// #2755 — what the scan cannot see: a helper imported from another module
// counts only if its name starts with `expect` (web/e2e/README.md states
// the rule), and a wrapper passed in some other way (a parameter, a
// re-export under a new name) is not followed.
//
// #2755 restored the 163 such tests the guard found in 13 older specs and
// retired the KNOWN_EMPTY ratchet that held their counts: no spec may
// contain one. A test whose feature does not exist yet is `test.fixme`
// with the reason beside it.

import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { dirname, join, sep } from "node:path";
import ts from "typescript";
import { describe, expect, test } from "vitest";

const WEB_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const SPEC_DIRS = ["e2e", "e2e-audit"];

const ASSERTION_CALL = /^expect/;
const COUNTED_MODIFIER = /^(only|fail)$/;

/** Local names of `imported` from any import: `{ expect as anonExpect }`. */
function importAliases(sf: ts.SourceFile, imported: string): Set<string> {
  const names = new Set([imported]);
  const visit = (node: ts.Node): void => {
    if (
      ts.isImportSpecifier(node) &&
      (node.propertyName ?? node.name).text === imported
    ) {
      names.add(node.name.text);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return names;
}

/** Names this file binds to a Playwright test function. */
function testNames(sf: ts.SourceFile): Set<string> {
  const names = importAliases(sf, "test");
  const visit = (node: ts.Node): void => {
    if (
      ts.isVariableDeclaration(node) &&
      ts.isIdentifier(node.name) &&
      node.initializer &&
      ts.isCallExpression(node.initializer) &&
      ts.isPropertyAccessExpression(node.initializer.expression) &&
      node.initializer.expression.name.text === "extend"
    ) {
      names.add(node.name.text);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return names;
}

/** Bodies of the functions declared in this file, by name. */
function localFunctions(sf: ts.SourceFile): Map<string, ts.Node> {
  const fns = new Map<string, ts.Node>();
  const visit = (node: ts.Node): void => {
    if (ts.isFunctionDeclaration(node) && node.name && node.body) {
      fns.set(node.name.text, node.body);
    }
    if (
      ts.isVariableDeclaration(node) &&
      ts.isIdentifier(node.name) &&
      node.initializer &&
      (ts.isArrowFunction(node.initializer) ||
        ts.isFunctionExpression(node.initializer))
    ) {
      fns.set(node.name.text, node.initializer.body);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return fns;
}

/** `test(...)`, `test.only(...)` or `test.fail(...)` under any test name. */
function isCountedTestCall(
  call: ts.CallExpression,
  names: Set<string>,
): boolean {
  const callee = call.expression;
  if (ts.isIdentifier(callee)) return names.has(callee.text);
  return (
    ts.isPropertyAccessExpression(callee) &&
    ts.isIdentifier(callee.expression) &&
    names.has(callee.expression.text) &&
    COUNTED_MODIFIER.test(callee.name.text)
  );
}

/** Titles of the counted tests in `source` whose body never calls expect. */
function testsWithoutExpect(fileName: string, source: string): string[] {
  const sf = ts.createSourceFile(
    fileName,
    source,
    ts.ScriptTarget.Latest,
    true,
  );
  const names = testNames(sf);
  const expects = importAliases(sf, "expect");
  const helpers = localFunctions(sf);
  const hits: string[] = [];
  // `seen` stops the walk looping through helpers that call each other.
  const hasAssertion = (node: ts.Node, seen: Set<string>): boolean => {
    if (ts.isCallExpression(node)) {
      const callee = node.expression.getText(sf);
      if (ASSERTION_CALL.test(callee)) return true;
      if (expects.has(callee.split(".")[0])) return true;
      const helper = helpers.get(callee);
      if (helper && !seen.has(callee)) {
        seen.add(callee);
        if (hasAssertion(helper, seen)) return true;
      }
    }
    return ts.forEachChild(node, (c) => hasAssertion(c, seen)) ?? false;
  };
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && isCountedTestCall(node, names)) {
      const [title] = node.arguments;
      const body = node.arguments.find(
        (a) => ts.isArrowFunction(a) || ts.isFunctionExpression(a),
      );
      if (title && body && !hasAssertion(body, new Set())) {
        hits.push(
          ts.isStringLiteralLike(title) ? title.text : title.getText(sf),
        );
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return hits;
}

/** Every `*.spec.ts` under `root/dir`, subdirectories included, relative to root. */
function specFiles(root: string, dir: string): string[] {
  return readdirSync(join(root, dir), { recursive: true })
    .map(String)
    .filter((entry) => entry.endsWith(".spec.ts"))
    .map((entry) => `${dir}/${entry.split(sep).join("/")}`)
    .sort();
}

function emptyTestCounts(): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const dir of SPEC_DIRS) {
    for (const rel of specFiles(WEB_ROOT, dir)) {
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
  test("no spec holds a test without an expect call", () => {
    expect(emptyTestCounts()).toEqual({});
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

  // #2755 — the three limitations the #2750 review named.
  test("the scan follows helpers declared in the same file", () => {
    const src = `
      async function assertRow(page) { await expect(page).toHaveURL(/x/); }
      const checkAll = async (page) => { await assertRow(page); };
      function loopA() { loopB(); }
      function loopB() { loopA(); }
      async function navigate(page) { await page.goto("/"); }
      test("direct helper", async ({ page }) => { await assertRow(page); });
      test("helper of a helper", async ({ page }) => { await checkAll(page); });
      test("helper with no assertion", async ({ page }) => { await navigate(page); });
      test("helpers that call each other", () => { loopA(); });
      test("imported helper not named expect", async ({ page }) => { await assertImported(page); });
    `;
    expect(testsWithoutExpect("x.spec.ts", src)).toEqual([
      "helper with no assertion",
      "helpers that call each other",
      "imported helper not named expect",
    ]);
  });

  test("the scan checks aliased and extended test functions", () => {
    const src = `
      import { test as base, test as authed } from "@playwright/test";
      const withSeed = base.extend({});
      authed("alias", async () => {});
      authed.only("alias only", async () => {});
      withSeed("extended", async () => {});
      withSeed.describe("group", () => {});
      authed.step("step", async () => {});
      authed("alias asserting", async () => { expect(1).toBe(1); });
    `;
    expect(testsWithoutExpect("x.spec.ts", src)).toEqual([
      "alias",
      "alias only",
      "extended",
    ]);
  });

  test("the scan counts an aliased expect as an assertion", () => {
    const src = `
      import { expect as anonExpect } from "@playwright/test";
      test("alias", async ({ page }) => { anonExpect(page).toBeTruthy(); });
      test("alias soft", async ({ page }) => { anonExpect.soft(page).toBeTruthy(); });
      test("unrelated", async () => { notExpect(1); });
    `;
    expect(testsWithoutExpect("x.spec.ts", src)).toEqual(["unrelated"]);
  });

  test("the scan ignores aliases it was not given", () => {
    const src = `
      const authed = something;
      authed("not a test name", async () => {});
    `;
    expect(testsWithoutExpect("x.spec.ts", src)).toEqual([]);
  });

  test("the scan checks tests whose title is not a string literal", () => {
    const src = `
      for (const r of ROUTES) {
        test(\`\${r.route} opens\`, async ({ page }) => { await page.goto(r.route); });
        test(r.title, async () => {});
        test(\`\${r.route} asserts\`, async () => { expect(r).toBeTruthy(); });
      }
    `;
    expect(testsWithoutExpect("x.spec.ts", src)).toEqual([
      "`${r.route} opens`",
      "r.title",
    ]);
  });

  test("spec discovery descends into subdirectories", () => {
    const root = mkdtempSync(join(tmpdir(), "e2e-tests-assert-"));
    try {
      mkdirSync(join(root, "e2e", "nested", "deeper"), { recursive: true });
      writeFileSync(join(root, "e2e", "top.spec.ts"), "");
      writeFileSync(join(root, "e2e", "nested", "deeper", "low.spec.ts"), "");
      writeFileSync(join(root, "e2e", "nested", "helper.ts"), "");
      expect(specFiles(root, "e2e")).toEqual([
        "e2e/nested/deeper/low.spec.ts",
        "e2e/top.spec.ts",
      ]);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
