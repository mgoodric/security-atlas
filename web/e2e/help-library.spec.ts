// #2154 — "How this works" help for the Library sidebar section.
//
// On every Library route, **?** opens that page's own module help
// (web/help/modules/<id>.md) with its body, its key terms and a "Read more"
// link to the matching docs page, and the page header carries at least one
// of the module's terms as a <Term>. The policies help also shows the policy
// lifecycle diagram.
//
// The **?** and the subtitle live in the page header, which each Library
// page renders in its loading, error and loaded states alike, so no data
// fetch is mocked.

import { expect, test } from "./fixtures";

const DOCS = "https://mgoodric.github.io/security-atlas/help/";

const ROUTES = [
  {
    route: "/policies",
    id: "policies",
    title: "Policies",
    // One source line: the renderer joins a paragraph's lines with <br/>.
    body: "A policy is a written rule your organisation commits to",
    term: "policy-acknowledgment",
    lifecycle: "policy",
  },
  {
    route: "/vendors",
    id: "vendors",
    title: "Vendors",
    body: "The vendor register lists the outside companies",
    term: "review-cadence",
  },
  {
    route: "/personnel-security",
    id: "personnel-security",
    title: "Personnel security",
    body: "People joining and leaving are one of the commonest causes",
    term: "personnel-checklist",
  },
  {
    route: "/questionnaires",
    id: "questionnaires",
    title: "Questionnaires",
    body: "Customers often send a security questionnaire before they buy",
    term: "questionnaire",
  },
  {
    route: "/catalog/scf",
    id: "scf-catalog",
    title: "SCF catalog",
    body: "free catalog of about 1,400 security controls",
    term: "scf",
  },
  {
    route: "/catalog/audited-under",
    id: "scf-catalog",
    title: "SCF catalog",
    body: "free catalog of about 1,400 security controls",
    term: "org-mapping",
  },
] as const;

test.describe("Library help (#2154)", () => {
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
