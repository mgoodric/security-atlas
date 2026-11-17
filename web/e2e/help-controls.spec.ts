// #2150 — "How this works" help for the Controls sidebar section.
//
// On every Controls route, **?** opens that page's own module help
// (web/help/modules/<id>.md) with its body, its key terms and a "Read more"
// link to the matching docs page, and the page header carries at least one
// of the module's terms as a <Term>. The Controls help shows the control
// state diagram and the Exceptions help the exception lifecycle.
//
// The **?** and the subtitle live in the page header, which each Controls
// page renders in its loading, error and loaded states alike, so no data
// fetch is mocked.

import { expect, test } from "./fixtures";

const DOCS = "https://mgoodric.github.io/security-atlas/help/";

const ROUTES = [
  {
    route: "/controls",
    id: "controls",
    title: "Controls",
    // One source line: the renderer joins a paragraph's lines with <br/>.
    body: "A control is one thing your organisation does to stay secure",
    term: "evaluation",
    lifecycle: "control_state",
  },
  {
    route: "/evidence",
    id: "evidence",
    title: "Evidence",
    body: "Evidence is the proof that a control is working",
    term: "evidence-ledger",
  },
  {
    route: "/exceptions",
    id: "exceptions",
    title: "Exceptions",
    body: "Sometimes a control cannot be met for a while",
    term: "exception",
    lifecycle: "exception",
  },
  {
    route: "/controls/checklist",
    id: "controls-checklist",
    title: "Implementation checklist",
    body: "Knowing which controls apply is not the same as knowing who has to do",
    term: "implementation-checklist",
  },
] as const;

test.describe("Controls help (#2150)", () => {
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
