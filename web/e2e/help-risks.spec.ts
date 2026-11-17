// #2151 — "How this works" help for the Risks sidebar section.
//
// On every Risks route, **?** opens that page's own module help
// (web/help/modules/<id>.md) with its body, its key terms and a "Read more"
// link to the matching docs page, and the page header carries at least one
// of the module's terms as a <Term>. The register help shows the risk
// treatment and open/closed diagrams, the action plans help the action plan
// lifecycle.
//
// The **?** and the subtitle live in the page header, which each Risks page
// renders in its loading, error and loaded states alike, so no data fetch is
// mocked.

import { expect, test } from "./fixtures";

const DOCS = "https://mgoodric.github.io/security-atlas/help/";

const ROUTES = [
  {
    route: "/risks",
    id: "risks",
    title: "Risk register",
    // One source line: the renderer joins a paragraph's lines with <br/>.
    body: "A risk is something that could go wrong and hurt your organisation",
    term: "treatment",
    lifecycles: ["risk_treatment", "risk_status"],
  },
  {
    route: "/risks/taxonomy",
    id: "risks",
    title: "Risk register",
    body: "A risk is something that could go wrong and hurt your organisation",
    term: "theme",
    lifecycles: ["risk_treatment", "risk_status"],
  },
  {
    route: "/risks/hierarchy",
    id: "risks-hierarchy",
    title: "Risk hierarchy",
    body: "The register is a flat list.",
    term: "org-unit",
    lifecycles: [],
  },
  {
    route: "/action-plans",
    id: "action-plans",
    title: "Action plans",
    body: "An action plan is a promise to fix something",
    term: "action-plan",
    lifecycles: ["action_plan"],
  },
] as const;

test.describe("Risks help (#2151)", () => {
  for (const r of ROUTES) {
    test(`${r.route} opens the ${r.id} help`, async ({ authedPage: page }) => {
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
      const diagrams = page.getByTestId("help-lifecycle");
      await expect(diagrams).toHaveCount(r.lifecycles.length);
      for (const [i, lifecycle] of r.lifecycles.entries()) {
        await expect(diagrams.nth(i)).toHaveAttribute(
          "data-lifecycle",
          lifecycle,
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
