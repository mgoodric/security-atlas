// #2149 — "How this works" help for the Program sidebar section.
//
// On every Program route, **?** opens that page's own module help
// (web/help/modules/<id>.md) with its body, its key terms and a "Read more"
// link to the matching docs page, and the page carries at least one of the
// module's terms as a <Term>. No Program page owns an exported lifecycle, so
// none of these panels shows a lifecycle diagram.
//
// The **?**, the subtitle and the dashboard's panel titles render in the
// loading, error and loaded states alike, so no data fetch is mocked.

import { expect, test } from "./fixtures";

const DOCS = "https://mgoodric.github.io/security-atlas/help/";

const ROUTES = [
  {
    route: "/dashboard",
    id: "dashboard",
    title: "Dashboard",
    // One source line: the renderer joins a paragraph's lines with <br/>.
    body: "The dashboard is where a working day starts.",
    term: "needs-attention",
  },
  {
    route: "/needs-attention",
    id: "dashboard",
    title: "Dashboard",
    body: "The dashboard is where a working day starts.",
    term: "owner",
  },
  {
    route: "/approvals",
    id: "approvals",
    title: "Approvals",
    body: "The person who asks for an approval cannot also give it.",
    term: "second-person-rule",
  },
  {
    route: "/calendar",
    id: "calendar",
    title: "Calendar",
    body: "A security program runs on dates",
    term: "calendar-subscription",
  },
  {
    route: "/dashboards/metrics",
    id: "metrics",
    title: "Metrics",
    body: "A board wants a handful of numbers that say whether security is getting",
    term: "board-kpi",
  },
  {
    route: "/activity",
    id: "activity",
    title: "Activity",
    body: "Every change in security-atlas leaves a record",
    term: "activity-log",
  },
] as const;

test.describe("Program help (#2149)", () => {
  for (const r of ROUTES) {
    test(`${r.route} opens the ${r.id} help`, async ({ authedPage: page }) => {
      await page.goto(r.route);

      // The page names at least one of the module's terms.
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
      await expect(page.getByTestId("help-lifecycle")).toHaveCount(0);
      await expect(page.getByTestId("help-panel-read-more")).toHaveAttribute(
        "href",
        `${DOCS}${r.id}/`,
      );

      await page.keyboard.press("Escape");
      await expect(panel).toHaveCount(0);
    });
  }
});
