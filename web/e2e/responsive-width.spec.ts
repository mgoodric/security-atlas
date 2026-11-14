// Phone and tablet width: no sideways scroll (#2068).
//
// The design audit (#2053) was walked at 1,316px and 1,200px; #2068
// finished it at 390px and 768px. This spec pins what that pass found
// working, so it can't regress, and records what it found broken as
// `test.fixme` cases tied to #2213. Fixing #2213 means switching those
// cases on.
//
// Two measurements, because the app shell has two horizontal scrollers:
//
//   - `<main>` is `overflow-y-auto`, which makes its x-axis scroll too.
//     Content wider than the column scrolls `<main>` sideways, not the
//     page. That is how the page-header action rows fail (#2213 part 2).
//   - The document itself. The top bar never collapses, so it widens the
//     whole page at 390px (#2213 part 1).
//
// `slice 277`'s `mobile-baseline.spec.ts` covers the hamburger and
// drawer; this spec is about width only.

import { expect, test } from "./fixtures";

const PHONE = { width: 390, height: 844 };
const TABLET = { width: 768, height: 1024 };

type Viewport = { width: number; height: number };

// Sideways overflow of `<main>`, in px. 0 when the column fits.
async function mainOverflow(page: import("@playwright/test").Page) {
  return page.evaluate(() => {
    const main = document.querySelector("main");
    if (!main) return -1;
    return main.scrollWidth - main.clientWidth;
  });
}

// Sideways overflow of the document, in px.
async function documentOverflow(page: import("@playwright/test").Page) {
  return page.evaluate(
    () =>
      Math.max(
        document.documentElement.scrollWidth,
        document.body.scrollWidth,
      ) - document.documentElement.clientWidth,
  );
}

async function open(
  page: import("@playwright/test").Page,
  path: string,
  viewport: Viewport,
) {
  await page.setViewportSize(viewport);
  await page.goto(path);
  await expect(page.locator("main")).toBeVisible();
  // Lists and panels fill in client-side; measure after they have.
  await page.waitForLoadState("networkidle");
}

// Pages whose column fits at both widths today.
const FITS = ["/dashboard", "/questionnaires", "/settings"];

// Pages whose column overflows (#2213), with the widths at which it does.
// Most are the page-header action row, measured on the demo dataset at
// #2068. `/evidence` is the Control filter: a native select sizes to its
// longest option, so one long control title pushes it past 390px (seen
// with the e2e fixtures, not the demo data).
const KNOWN_OVERFLOWS: { path: string; at: Viewport[] }[] = [
  { path: "/controls", at: [PHONE, TABLET] },
  { path: "/audits", at: [PHONE, TABLET] },
  { path: "/risks", at: [PHONE] },
  { path: "/policies", at: [PHONE] },
  { path: "/vendors", at: [PHONE] },
  { path: "/evidence", at: [PHONE] },
];

for (const viewport of [PHONE, TABLET]) {
  test.describe(`at ${viewport.width}px`, () => {
    for (const path of FITS) {
      test(`${path}: the content column does not scroll sideways`, async ({
        authedPage: page,
      }) => {
        await open(page, path, viewport);
        expect(await mainOverflow(page)).toBeLessThanOrEqual(1);
      });
    }

    for (const { path, at } of KNOWN_OVERFLOWS) {
      const broken = at.includes(viewport);
      test(`${path}: the content column does not scroll sideways`, async ({
        authedPage: page,
      }) => {
        test.fixme(broken, "#2213: the column overflows at this width");
        await open(page, path, viewport);
        expect(await mainOverflow(page)).toBeLessThanOrEqual(1);
      });
    }

    test("/dashboard: the page does not scroll sideways", async ({
      authedPage: page,
    }) => {
      test.fixme(true, "#2213: the top bar doesn't collapse below 1,200px");
      await open(page, "/dashboard", viewport);
      expect(await documentOverflow(page)).toBeLessThanOrEqual(1);
    });
  });
}

// The sign-in page has no app shell; it fits at phone width today.
test("/login fits 390px without sideways scroll", async ({ page }) => {
  await page.setViewportSize(PHONE);
  await page.goto("/login");
  await page.waitForLoadState("networkidle");
  expect(await documentOverflow(page)).toBeLessThanOrEqual(1);
});
