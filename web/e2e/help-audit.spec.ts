// #2152 — "How this works" help for the Audit sidebar section.
//
// On every Audit route, **?** opens that page's own module help
// (web/help/modules/<id>.md) with its body, its key terms and a "Read more"
// link to the matching docs page, and the page header carries at least one
// of the module's terms as a <Term>. The audit-period and framework-scope
// helps also show their lifecycle diagram.
//
// The **?** and the subtitle live in the page header. Board packs and
// vendor claims sit behind optional modules and CSF needs a loaded NIST CSF
// version, so their data fetches are route-mocked (the b219 lesson: never
// rely on the shared seed) to keep the header rendering whatever the
// deployment has switched on.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

const DOCS = "https://mgoodric.github.io/security-atlas/help/";
const CSF_FV = "11111111-1111-4111-8111-111111111111";

async function mockModuleData(page: Page) {
  await page.route("**/api/csf/gap*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        framework_version_id: CSF_FV,
        framework_name: "NIST Cybersecurity Framework",
        framework_version: "2.0",
        gap: [],
        gap_count: 0,
        tier_rating: null,
      }),
    }),
  );
}

const ROUTES = [
  {
    route: "/audits",
    id: "audits",
    title: "Audit periods",
    body: "An audit period is the window of time an external auditor reviews",
    term: "audit-period",
    lifecycle: "audit_period",
  },
  {
    route: "/framework-scopes",
    id: "framework-scopes",
    title: "Framework scopes",
    body: "A framework scope says which parts of your organisation",
    term: "statement-of-applicability",
    lifecycle: "framework_scope",
  },
  {
    route: "/board-packs",
    id: "board-packs",
    title: "Board packs",
    body: "A board pack is the quarterly report you take to your board",
    term: "board-pack",
  },
  {
    route: `/csf/${CSF_FV}`,
    id: "csf",
    title: "CSF assessment",
    body: "how far is it from where you want to be",
    term: "csf-tier",
  },
  {
    route: "/oscal/component-definitions",
    id: "vendor-claims",
    title: "Vendor claims",
    // One source line, so the match holds however the .md wraps (#2455).
    body: "statements is a vendor claim",
    term: "vendor-claim",
  },
] as const;

test.describe("Audit help (#2152)", () => {
  for (const r of ROUTES) {
    test(`${r.route} opens the ${r.id} help`, async ({ authedPage: page }) => {
      await mockModuleData(page);
      await page.goto(r.route);

      // The page header names at least one of the module's terms.
      await expect(
        page.getByTestId(`term-${r.term}-trigger`).first(),
      ).toBeVisible();

      const button = page.getByTestId("help-button");
      await expect(button).toHaveAccessibleName(`How this works: ${r.title}`);
      await button.click();

      const panel = page.getByTestId("help-panel");
      await expect(panel).toBeVisible();
      await expect(
        panel.getByRole("heading", { name: r.title, exact: true }),
      ).toBeVisible();
      await expect(page.getByTestId("help-panel-body")).toContainText(r.body);
      await expect(page.getByTestId("help-panel-terms")).not.toBeEmpty();
      if ("lifecycle" in r) {
        await expect(page.getByTestId("help-lifecycle")).toHaveAttribute(
          "data-lifecycle",
          r.lifecycle,
        );
      }
      await expect(page.getByTestId("help-panel-read-more")).toHaveAttribute(
        "href",
        `${DOCS}${r.id}/`,
      );

      await page.keyboard.press("Escape");
      await expect(panel).toHaveCount(0);
    });
  }
});
