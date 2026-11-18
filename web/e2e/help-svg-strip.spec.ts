// #2148 — the SVG stripper the diagram renderer runs in the browser.
//
// `scripts/render-help-diagrams.mjs` passes `stripActiveSvg` to the
// Chromium page that rendered a diagram. This runs the same function in
// a blank page, on hostile input, and checks nothing active survives and
// nothing harmless is lost. No app, no sign-in.

import { expect, test } from "@playwright/test";

import { SVG_ELEMENTS, unsafeSvgParts } from "../lib/help/diagram-build";
import { stripActiveSvg } from "../lib/help/svg-strip";
import { UNSAFE_SVG_FIXTURES } from "../lib/help/unsafe-svg-fixtures";

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

  // #2429 — the fixtures the Node gate refuses in diagrams.test.ts.
  for (const f of UNSAFE_SVG_FIXTURES) {
    test(`strips ${f.name}`, async ({ page }) => {
      const run = page.evaluate(stripActiveSvg, f.svg);
      if (f.browserThrows) {
        await expect(run).rejects.toThrow(f.browserThrows);
        return;
      }
      const clean = await run;
      expect(unsafeSvgParts(clean)).toEqual([]);
      expect(clean).not.toMatch(/evil/i);
    });
  }

  test("keeps every allow-listed element and fragment reference", async ({
    page,
  }) => {
    const body = SVG_ELEMENTS.filter((e) => e !== "svg")
      .map((e) => `<${e} id="${e}-x"/>`)
      .join("");
    const dirty =
      `<svg ${NS} ${XLINK}><style>@keyframes d{to{opacity:0}}#l{fill:url(#g)}</style>` +
      `${body}<rect fill="url(#g)" style="stroke: url('#g')"/><use xlink:href="#g"/></svg>`;
    const clean = await page.evaluate(stripActiveSvg, dirty);
    expect(unsafeSvgParts(clean)).toEqual([]);
    for (const e of SVG_ELEMENTS.filter((e) => e !== "svg")) {
      expect(clean).toContain(`<${e} id="${e}-x"`);
    }
    expect(clean).toContain("@keyframes d");
    expect(clean).toContain('fill="url(#g)"');
    expect(clean).toContain("stroke: url('#g')");
    expect(clean).toContain('xlink:href="#g"');
  });

  test("refuses input that is not well-formed XML", async ({ page }) => {
    await expect(
      page.evaluate(stripActiveSvg, "<svg><g></svg>"),
    ).rejects.toThrow(/does not parse/);
  });
});
