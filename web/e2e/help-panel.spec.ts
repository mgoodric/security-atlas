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
