// #2143 — the "How this works" panel: PageShell's **?** action.
//
// On Controls, **?** opens a modal side panel with the Controls help from
// web/help/modules/controls.md; focus moves into it, Escape closes it and
// focus returns to the button; axe passes on the open panel. A page no
// help module covers shows no **?**.
//
// The **?** lives in the page header, which renders whatever the list
// fetch returns, so the spec needs no seed data.

import AxeBuilder from "@axe-core/playwright";

import { expect, test } from "./fixtures";

const DOCS_PAGE = "https://mgoodric.github.io/security-atlas/help/controls/";

test.describe("How this works panel (#2143)", () => {
  test("on Controls, ? opens the Controls help; Escape closes it and focus returns", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    const button = page.getByTestId("help-button");
    const panel = page.getByTestId("help-panel");
    await expect(button).toBeVisible();
    await expect(button).toHaveAccessibleName("How this works: Controls");
    await expect(panel).toHaveCount(0);

    await button.click();
    await expect(panel).toBeVisible();
    await expect(panel).toHaveAttribute("role", "dialog");
    await expect(panel).toHaveAttribute("aria-modal", "true");
    await expect(
      panel.getByRole("heading", { name: "Controls", exact: true }),
    ).toBeVisible();
    await expect(page.getByTestId("help-panel-body")).toContainText(
      "A control is one thing your organisation does to stay secure",
    );
    await expect(page.getByTestId("help-panel-terms")).toContainText(
      "SCF anchor",
    );
    await expect(page.getByTestId("help-panel-read-more")).toHaveAttribute(
      "href",
      DOCS_PAGE,
    );

    // Focus moved into the panel.
    const focusInPanel = () =>
      panel.evaluate((el) => el.contains(document.activeElement));
    await expect.poll(focusInPanel).toBe(true);

    // Tab keeps focus inside the open panel (the focus trap). Past the last
    // link, Tab lands on base-ui's focus guard, which hands focus back to
    // the first element a tick later, so each step polls.
    for (let i = 0; i < 12; i += 1) {
      await page.keyboard.press("Tab");
      await expect.poll(focusInPanel).toBe(true);
    }

    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(button).toBeFocused();
  });

  test("the close button closes the panel and returns focus", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    const button = page.getByTestId("help-button");
    await button.click();
    await page.getByTestId("help-panel-close").click();
    await expect(page.getByTestId("help-panel")).toHaveCount(0);
    await expect(button).toBeFocused();
  });

  test("axe passes on the open panel", async ({ authedPage: page }) => {
    await page.goto("/controls");
    await page.getByTestId("help-button").click();
    await expect(page.getByTestId("help-panel")).toBeVisible();
    const results = await new AxeBuilder({ page })
      .include('[data-testid="help-panel"]')
      .analyze();
    expect(results.violations).toEqual([]);
  });

  // #2148 — the exception lifecycle diagram, in the Exceptions help since
  // #2150: the pre-rendered SVG for the current theme loads and shows, the
  // other is hidden, and the image is described by the list of its states
  // and transitions, which a screen reader reaches.
  for (const theme of ["light", "dark"] as const) {
    test(`the lifecycle diagram renders in ${theme} mode with its text alternative`, async ({
      authedPage: page,
    }) => {
      await page.emulateMedia({ colorScheme: theme });
      await page.addInitScript(
        ([key, value]) => window.localStorage.setItem(key, value),
        ["security-atlas.settings.theme", theme] as const,
      );
      await page.goto("/exceptions");
      await expect
        .poll(() =>
          page.evaluate(() =>
            document.documentElement.classList.contains("dark"),
          ),
        )
        .toBe(theme === "dark");
      await page.getByTestId("help-button").click();

      const figure = page.locator(
        '[data-testid="help-lifecycle"][data-lifecycle="exception"]',
      );
      const shown = figure.getByTestId(`help-lifecycle-${theme}`);
      const hidden = figure.getByTestId(
        `help-lifecycle-${theme === "dark" ? "light" : "dark"}`,
      );
      await expect(shown).toBeVisible();
      await expect(hidden).toBeHidden();
      await expect(shown).toHaveAttribute(
        "src",
        `/help/diagrams/exceptions-exception-${theme}.svg`,
      );
      // The SVG actually loaded and laid out, not a broken image.
      await expect
        .poll(() =>
          shown.evaluate((img: HTMLImageElement) =>
            img.complete ? img.naturalWidth : 0,
          ),
        )
        .toBeGreaterThan(0);

      // Assistive technology: the visible image is an img with a name, and
      // its description is the full list of states and transitions.
      await expect(
        page.getByRole("img", { name: "Exception lifecycle" }),
      ).toHaveCount(1);
      await expect(shown).toHaveAccessibleName(
        "Exception lifecycle: 6 states and 5 transitions, listed below.",
      );
      await expect(shown).toHaveAccessibleDescription(
        /requested \(starts here on request\).*active to expired: expire, done automatically by the system.*active to revoked: revoke/,
      );

      // The same list is one click away for sighted users.
      await figure.getByText("The diagram as text").click();
      await expect(figure.getByTestId("help-lifecycle-text")).toContainText(
        "approved to active: activate",
      );

      const results = await new AxeBuilder({ page })
        .include('[data-testid="help-panel"]')
        .analyze();
      expect(results.violations).toEqual([]);
    });
  }

  test("a page with no help module shows no ?", async ({
    authedPage: page,
  }) => {
    await page.goto("/risks");
    await expect(
      page.getByRole("heading", { level: 1, name: "Risk register" }),
    ).toBeVisible();
    await expect(page.getByTestId("help-button")).toHaveCount(0);
  });
});
