// #2148 — the SVG stripper the diagram renderer runs in the browser.
//
// `scripts/render-help-diagrams.mjs` passes `stripActiveSvg` to the
// Chromium page that rendered a diagram. This runs the same function in
// a blank page, on hostile input, and checks nothing active survives and
// nothing harmless is lost. No app, no sign-in.

import { expect, test } from "@playwright/test";

import { unsafeSvgParts } from "../lib/help/diagram-build";
import { stripActiveSvg } from "../lib/help/svg-strip";

const NS = 'xmlns="http://www.w3.org/2000/svg"';
const XLINK = 'xmlns:xlink="http://www.w3.org/1999/xlink"';

test.describe("help diagram SVG stripping (#2148)", () => {
  test("removes scripts, event handlers and non-fragment links", async ({
    page,
  }) => {
    const dirty =
      `<svg ${NS} ${XLINK} onload="alert(1)"><script>alert(2)</script><script href="x.js"/>` +
      `<g ONCLICK="go()" class="node"><a xlink:href="javascript:alert(3)"><text>a</text></a>` +
      `<a href="https://evil.example">b</a><use href="#arrow"/></g></svg>`;
    const clean = await page.evaluate(stripActiveSvg, dirty);
    expect(unsafeSvgParts(clean)).toEqual([]);
    expect(clean).not.toMatch(/script|onload|onclick|alert/i);
    expect(clean).toContain('<g class="node">');
    expect(clean).toContain("<text>a</text>");
    expect(clean).toContain('<use href="#arrow"/>');
  });

  test("leaves no script that a text-level pass would reassemble", async ({
    page,
  }) => {
    const dirty =
      `<svg ${NS}><text>&lt;scr</text><script>x</script><text>ipt&gt;</text>` +
      `<script type="a"><![CDATA[alert(1)]]></script></svg>`;
    const clean = await page.evaluate(stripActiveSvg, dirty);
    expect(unsafeSvgParts(clean)).toEqual([]);
    expect(clean).not.toMatch(/alert/);
  });

  test("refuses input that is not well-formed XML", async ({ page }) => {
    await expect(
      page.evaluate(stripActiveSvg, "<svg><g></svg>"),
    ).rejects.toThrow(/does not parse/);
  });
});
