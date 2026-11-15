// Phone and tablet width: no sideways scroll (#2068).
//
// The design audit (#2053) was walked at 1,316px and 1,200px; #2068
// finished it at 390px and 768px. This spec pins what that pass found
// working, so it can't regress. What it found broken was recorded as
// `test.fixme` cases tied to #2213; #2213 fixed them and switched them
// on.
//
// Two measurements, because the app shell has two horizontal scrollers:
//
//   - `<main>` is `overflow-y-auto`, which makes its x-axis scroll too.
//     Content wider than the column scrolls `<main>` sideways, not the
//     page. That is how the page-header action rows failed (#2213 part 2).
//   - The document itself. The top bar did not collapse, so it widened
//     the whole page at 390px and 768px (#2213 part 1). It now folds
//     below `lg`.
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

// Signed-in pages checked at both widths. The first three fitted before
// #2213; the rest are the ones #2068 found overflowing. Most of those
// were the page-header action row; `/evidence` was the Control filter (a
// native select sizes to its longest option, so one long control title
// pushed it past 390px with the e2e fixtures); `/action-plans/new` was
// its linked-entity fieldsets; `/risks/hierarchy` kept three columns at
// 768px; `/audit-log` rendered outside the app shell.
const PAGES = [
  "/dashboard",
  "/questionnaires",
  "/settings",
  "/controls",
  "/audits",
  "/risks",
  "/policies",
  "/vendors",
  "/evidence",
  "/action-plans/new",
  "/risks/hierarchy",
  "/audit-log",
];

for (const viewport of [PHONE, TABLET]) {
  test.describe(`at ${viewport.width}px`, () => {
    for (const path of PAGES) {
      test(`${path}: neither the page nor the content column scrolls sideways`, async ({
        authedPage: page,
      }) => {
        await open(page, path, viewport);
        expect(await mainOverflow(page)).toBeLessThanOrEqual(1);
        expect(await documentOverflow(page)).toBeLessThanOrEqual(1);
      });
    }

    // #2213 — below `lg` the top bar folds search behind an icon and the
    // avatar + Sign out behind an account button. Both have to work from
    // the keyboard alone.
    test("search unfolds from its icon and folds back on Escape", async ({
      authedPage: page,
    }) => {
      await open(page, "/dashboard", viewport);
      const toggle = page.getByTestId("global-search-toggle");
      const input = page.getByTestId("global-search-input");
      await expect(input).toBeHidden();
      await toggle.focus();
      await page.keyboard.press("Enter");
      await expect(input).toBeFocused();
      expect(await documentOverflow(page)).toBeLessThanOrEqual(1);
      await page.keyboard.press("Escape");
      await expect(input).toBeHidden();
      await expect(toggle).toBeFocused();
    });

    test("Sign out is reachable through the account menu", async ({
      authedPage: page,
    }) => {
      await open(page, "/dashboard", viewport);
      const trigger = page.getByTestId("account-menu-trigger");
      const signOut = page.getByRole("button", { name: "Sign out" });
      await expect(signOut).toBeHidden();
      await trigger.focus();
      await page.keyboard.press("Enter");
      await expect(trigger).toHaveAttribute("aria-expanded", "true");
      await page.keyboard.press("Tab");
      await expect(signOut).toBeFocused();
      await page.keyboard.press("Escape");
      await expect(signOut).toBeHidden();
      await expect(trigger).toBeFocused();
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
