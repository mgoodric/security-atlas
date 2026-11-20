// #2592 — "How this works" help for the auditor workspace and a
// requirement's page, the last routes #2156's sweep found without help.
//
// On each route, **?** opens the page's module help
// (web/help/modules/<id>.md) with its body, its key terms and a "Read more"
// link to the matching docs page, and the page carries at least one of the
// module's terms as a <Term>. Neither page owns an exported lifecycle, so
// neither panel shows a lifecycle diagram.
//
// The workspace resolves the caller's audit period server-side, so it is
// not route-mocked. The shared test user holds no assignment and gets the
// "No audit period assigned" state, which names the assigned-auditor term;
// with an assignment, the period bar names the evidence horizon instead.
// Either way the **?** is there: PageShell carries it in the first state,
// the period bar in the second. audit-workspace.spec.ts checks the second
// for an assigned auditor. A requirement's page renders its header whatever
// the coverage fetch returns, so an all-zero id serves.

import { expect, test } from "./fixtures";

const DOCS = "https://mgoodric.github.io/security-atlas/help/";
const ZERO = "00000000-0000-0000-0000-000000000000";

const WORKSPACE_TERMS = ["assigned-auditor", "evidence-horizon"];

const ROUTES = [
  {
    route: "/audit",
    id: "audit-workspace",
    title: "Audit workspace",
    // One source line, so the match holds however the .md wraps (#2455).
    body: "Pick a control on the left, or add one by its id.",
    terms: WORKSPACE_TERMS,
  },
  {
    route: `/audit/${ZERO}`,
    id: "audit-workspace",
    title: "Audit workspace",
    body: "Pick a control on the left, or add one by its id.",
    terms: WORKSPACE_TERMS,
  },
  {
    route: `/requirements/${ZERO}`,
    id: "requirements",
    title: "Requirement detail",
    body: "This page shows that chain for one requirement.",
    terms: ["requirement"],
  },
] as const;

test.describe("Audit workspace and requirement help (#2592)", () => {
  for (const r of ROUTES) {
    test(`${r.route} opens the ${r.id} help`, async ({ authedPage: page }) => {
      await page.goto(r.route);

      // The page names at least one of the module's terms.
      const term = page.locator(
        r.terms.map((t) => `[data-testid="term-${t}-trigger"]`).join(", "),
      );
      await expect(term.first()).toBeVisible();

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
