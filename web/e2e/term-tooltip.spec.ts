// #2144 — <Term> tooltips: the short definition shows on hover, on
// keyboard focus and on tap; the link opens the docs Terms page at the
// term's entry; Escape closes it without moving focus; axe passes on the
// open tooltip.
//
// Exercised on the risk register's Residual header (a first adoption
// point), which renders client-side, so the spec is hermetic: the list
// BFF and org-units fetch are route-mocked, and the docs site is stubbed
// at the network layer so the "opens the Terms page" check doesn't depend
// on GitHub Pages being reachable from CI. Neutral test strings only.

import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

const TERM = "residual-risk";
const TERMS_PAGE = "https://mgoodric.github.io/security-atlas/terms/";
const SHORT_DEF_START = "How much risk is left once your controls are counted.";

function mockRisk() {
  return {
    id: "00000000-0000-4000-8000-0000000021aa",
    title: "Risk Term Fixture",
    description: "Neutral risk for the term tooltip spec",
    category: "operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 3, impact: 4 },
    treatment: "mitigate",
    treatment_owner: "owner-t",
    residual_score: { likelihood: 2, impact: 3 },
    review_due_at: "2026-12-01T00:00:00Z",
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    themes: [],
    severity: 12,
  };
}

async function openRegister(page: Page) {
  await page.route("**/api/risks-hierarchy/org-units**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ org_units: [] }),
    }),
  );
  await page.route(/\/api\/risks(\?.*)?$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ risks: [mockRisk()] }),
    }),
  );
  await page.goto("/risks");
  // The desktop table; the mobile card stack repeats the header labels.
  const desktop = page.locator('[data-testid="list-table-wrap"]');
  const trigger = desktop.getByTestId(`term-${TERM}-trigger`);
  const popup = desktop.getByTestId(`term-${TERM}-popup`);
  await expect(trigger).toBeVisible();
  await expect(popup).toBeHidden();
  return { desktop, trigger, popup };
}

test.describe("<Term> tooltip (#2144)", () => {
  test("shows the definition on hover and hides it when the pointer leaves", async ({
    authedPage,
  }) => {
    const { trigger, popup } = await openRegister(authedPage);
    await trigger.hover();
    await expect(popup).toBeVisible();
    await expect(popup).toContainText(SHORT_DEF_START);
    await authedPage.mouse.move(0, 0);
    await expect(popup).toBeHidden();
  });

  test("shows the definition on keyboard focus, is described by it, and Escape closes it without moving focus", async ({
    authedPage,
  }) => {
    const { trigger, popup, desktop } = await openRegister(authedPage);
    await trigger.focus();
    await expect(popup).toBeVisible();
    // The definition is the term's accessible description.
    await expect(trigger).toHaveAccessibleDescription(
      new RegExp(`^${SHORT_DEF_START}`),
    );

    // Tab reaches the link inside the open tooltip; it stays open.
    await authedPage.keyboard.press("Tab");
    await expect(desktop.getByTestId(`term-${TERM}-link`)).toBeFocused();
    await expect(popup).toBeVisible();

    // Back to the term, then Escape: closed, and focus is still on it.
    await authedPage.keyboard.press("Shift+Tab");
    await expect(trigger).toBeFocused();
    await authedPage.keyboard.press("Escape");
    await expect(popup).toBeHidden();
    await expect(trigger).toBeFocused();

    // Escape holds until focus leaves: Tab away (the closed tooltip's link
    // isn't tabbable) and back, and it opens again.
    await authedPage.keyboard.press("Tab");
    await expect(trigger).not.toBeFocused();
    await authedPage.keyboard.press("Shift+Tab");
    await expect(trigger).toBeFocused();
    await expect(popup).toBeVisible();

    // Tabbing past the link closes it.
    await authedPage.keyboard.press("Tab");
    await authedPage.keyboard.press("Tab");
    await expect(popup).toBeHidden();
  });

  test("the link opens the Terms page at the term's entry", async ({
    authedPage,
  }) => {
    await authedPage.context().route(`${TERMS_PAGE}**`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "text/html",
        body: `<!doctype html><title>Terms</title><h2 id="${TERM}">Residual risk</h2>`,
      }),
    );
    const { trigger, desktop } = await openRegister(authedPage);
    await trigger.hover();
    const link = desktop.getByTestId(`term-${TERM}-link`);
    await expect(link).toHaveAttribute("href", `${TERMS_PAGE}#${TERM}`);
    await expect(link).toHaveAttribute("target", "_blank");
    await expect(link).toHaveAttribute("rel", "noopener noreferrer");

    const [terms] = await Promise.all([
      authedPage.context().waitForEvent("page"),
      link.click(),
    ]);
    await terms.waitForLoadState();
    expect(terms.url()).toBe(`${TERMS_PAGE}#${TERM}`);
    await expect(terms.locator(`#${TERM}`)).toHaveText("Residual risk");
  });

  test("axe finds no violations in the open tooltip", async ({
    authedPage,
  }) => {
    const { trigger, popup } = await openRegister(authedPage);
    await trigger.focus();
    await expect(popup).toBeVisible();
    const results = await new AxeBuilder({ page: authedPage })
      .include(`[data-testid="list-table-wrap"] [data-testid="term-${TERM}"]`)
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
      .analyze();
    expect(results.violations).toEqual([]);
  });
});

test.describe("<Term> tooltip on touch (#2144)", () => {
  test.use({ hasTouch: true });

  test("a tap shows the definition, a second tap hides it", async ({
    authedPage,
  }) => {
    const { trigger, popup } = await openRegister(authedPage);
    await trigger.tap();
    await expect(popup).toBeVisible();
    await expect(popup).toContainText(SHORT_DEF_START);
    await trigger.tap();
    await expect(popup).toBeHidden();
  });
});
