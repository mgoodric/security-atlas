// #2097 — self-test for the axe gate the route sweep enforces.
//
// A deliberately failing fixture: a page with a serious violation
// (low-contrast text) and a critical one (an unlabelled input). The
// gate must reject it, naming both rules. A clean fixture must pass, so
// the gate is not simply always red.
//
// The fixtures are inline HTML loaded with `page.setContent`, not app
// routes: nothing here exists in `web/app`, so nothing ships in the
// production build, and no sign-in is needed.

import { expect, test } from "@playwright/test";

import { expectNoSeriousAxe, seriousAxeViolations } from "./axe-gate";

const PAGE = (body: string) =>
  `<!doctype html><html lang="en"><head><title>axe gate fixture</title></head>` +
  `<body><main><h1>Fixture</h1>${body}</main></body></html>`;

const FAILING = PAGE(
  `<p style="color:#bbbbbb;background:#ffffff">Low-contrast text</p>` +
    `<input type="text">`,
);

const CLEAN = PAGE(
  `<p style="color:#111111;background:#ffffff">Readable text</p>` +
    `<label for="q">Query</label><input id="q" type="text">`,
);

test.describe("axe gate (#2097)", () => {
  test("rejects a page with serious and critical violations", async ({
    page,
  }) => {
    await page.setContent(FAILING);
    const findings = await seriousAxeViolations(page);
    expect(findings.map((f) => f.id).sort()).toEqual(
      ["color-contrast", "label"].sort(),
    );
    expect(() => expectNoSeriousAxe(findings, "failing fixture")).toThrow(
      /2 serious\/critical axe violation\(s\)[\s\S]*color-contrast[\s\S]*label/,
    );
  });

  test("passes a clean page", async ({ page }) => {
    await page.setContent(CLEAN);
    expectNoSeriousAxe(await seriousAxeViolations(page), "clean fixture");
  });
});
