// #2142 — the /settings theme choice: System / Light / Dark, System the
// default, persisted per user, with swatches drawn from the theme tokens.
//
// The stored theme is per user, and the rest of the suite runs
// fullyParallel as the one demo user (board-pack-theme-contrast.spec.ts
// forces dark and light through the browser cache at the same time). So
// this spec signs in as a user of its own, seeded by
// fixtures/e2e/theme.sql with the theme reset to never-chosen, and runs
// serially because its tests share that user's stored theme. It took over
// settings.spec.ts AC-2 (persists across reload) and AC-12 (Dark actually
// themes the page), which wrote the demo user's theme once the choice
// moved server-side.

import { test as base, expect, type Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";

import { issueTestJWT } from "./global-setup";
import { THEME_USER_ID, seedFromFixture } from "./seed";

const THEME_STORAGE_KEY = "security-atlas.settings.theme";
const DARK_CLASS = /(^|\s)dark(\s|$)/;

let bearer = "";

const test = base.extend<{ themePage: Page }>({
  themePage: async ({ page, baseURL }, use) => {
    const url = new URL(baseURL ?? "http://localhost:3000");
    await page.context().addCookies([
      {
        name: ATLAS_JWT_COOKIE,
        value: bearer,
        domain: url.hostname,
        path: "/",
        httpOnly: true,
        secure: url.protocol === "https:",
        sameSite: "Lax",
      },
    ]);
    await use(page);
  },
});

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  seedFromFixture("theme");
  bearer = await issueTestJWT(THEME_USER_ID);
});

async function bodyBackground(page: Page): Promise<string> {
  return page.evaluate(
    () => window.getComputedStyle(document.body).backgroundColor,
  );
}

async function backgroundOf(page: Page, testId: string): Promise<string> {
  return page
    .getByTestId(testId)
    .evaluate((el) => window.getComputedStyle(el).backgroundColor);
}

// Choose a theme and wait for the account copy to be written, so a reload
// reads the new value back from the server.
async function choose(page: Page, theme: "system" | "light" | "dark") {
  const saved = page.waitForResponse(
    (r) => r.url().endsWith("/api/me") && r.request().method() === "PATCH",
  );
  await page.getByTestId(`settings-theme-option-${theme}`).click();
  expect((await saved).status()).toBe(200);
}

async function clearThemeCache(page: Page) {
  await page.evaluate(
    (key) => window.localStorage.removeItem(key),
    THEME_STORAGE_KEY,
  );
}

test.describe("/settings theme (#2142)", () => {
  test("offers System, Light and Dark, in that order", async ({
    themePage: page,
  }) => {
    await page.goto("/settings");
    const options = page
      .getByRole("radiogroup", { name: "Theme" })
      .getByRole("radio");
    await expect(options).toHaveText([/^System/, /^Light/, /^Dark/]);
  });

  test("the default is System, and it follows the OS", async ({
    themePage: page,
  }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/settings");
    await expect(
      page.getByTestId("settings-theme-option-system"),
    ).toHaveAttribute("data-selected", "true");
    await expect(page.locator("html")).toHaveClass(DARK_CLASS);

    // The OS flips while the page is open; the page follows without a reload.
    await page.emulateMedia({ colorScheme: "light" });
    await expect(page.locator("html")).not.toHaveClass(DARK_CLASS);
  });

  test("the swatches are the app's own light and dark backgrounds", async ({
    themePage: page,
  }) => {
    // Theme is still System here. In OS-light the page paints the light
    // token; the light pane must match it.
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/settings");
    await expect(page.locator("html")).not.toHaveClass(DARK_CLASS);
    const lightPage = await bodyBackground(page);
    expect(await backgroundOf(page, "settings-theme-swatch-light-light")).toBe(
      lightPage,
    );
    // Read the dark pane while the page is LIGHT: it must still be the
    // dark token, not whatever the page is in.
    const darkPane = await backgroundOf(
      page,
      "settings-theme-swatch-dark-dark",
    );
    expect(await backgroundOf(page, "settings-theme-swatch-system-dark")).toBe(
      darkPane,
    );
    expect(await backgroundOf(page, "settings-theme-swatch-system-light")).toBe(
      lightPage,
    );

    // Now the page itself goes dark: its background is the pane's.
    await page.emulateMedia({ colorScheme: "dark" });
    await expect(page.locator("html")).toHaveClass(DARK_CLASS);
    expect(await bodyBackground(page)).toBe(darkPane);

    // Neutral, not navy: equal channels.
    // Chromium serializes the computed colour in its authored space
    // (oklch/lab), so paint it to a pixel to read sRGB channels.
    const [r, g, b] = await page.evaluate((color) => {
      const ctx = document.createElement("canvas").getContext("2d")!;
      ctx.fillStyle = color;
      ctx.fillRect(0, 0, 1, 1);
      return Array.from(ctx.getImageData(0, 0, 1, 1).data.slice(0, 3));
    }, darkPane);
    expect(Math.abs(r - g)).toBeLessThanOrEqual(1);
    expect(Math.abs(g - b)).toBeLessThanOrEqual(1);
    expect(r).toBeLessThan(64);
  });

  test("Dark persists across reloads, from the account", async ({
    themePage: page,
  }) => {
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto("/settings");
    const lightBg = await bodyBackground(page);
    await choose(page, "dark");
    await expect(page.locator("html")).toHaveClass(DARK_CLASS);
    expect(await bodyBackground(page)).not.toBe(lightBg);

    await page.reload();
    await expect(
      page.getByTestId("settings-theme-option-dark"),
    ).toHaveAttribute("data-selected", "true");
    await expect(page.locator("html")).toHaveClass(DARK_CLASS);

    // Another page, then a browser with no cached choice: the account copy
    // brings Dark back.
    await page.goto("/dashboard");
    await expect(page.locator("html")).toHaveClass(DARK_CLASS);
    await clearThemeCache(page);
    await page.reload();
    await expect(page.locator("html")).toHaveClass(DARK_CLASS);
    await expect
      .poll(() =>
        page.evaluate((k) => window.localStorage.getItem(k), THEME_STORAGE_KEY),
      )
      .toBe("dark");
  });

  test("Light persists across reloads, even with the OS in dark", async ({
    themePage: page,
  }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/settings");
    await choose(page, "light");
    await expect(page.locator("html")).not.toHaveClass(DARK_CLASS);

    await page.reload();
    await expect(
      page.getByTestId("settings-theme-option-light"),
    ).toHaveAttribute("data-selected", "true");
    await expect(page.locator("html")).not.toHaveClass(DARK_CLASS);

    await clearThemeCache(page);
    await page.reload();
    await expect(
      page.getByTestId("settings-theme-option-light"),
    ).toHaveAttribute("data-selected", "true");
    await expect(page.locator("html")).not.toHaveClass(DARK_CLASS);
    await expect
      .poll(() =>
        page.evaluate((k) => window.localStorage.getItem(k), THEME_STORAGE_KEY),
      )
      .toBe("light");
  });
});
