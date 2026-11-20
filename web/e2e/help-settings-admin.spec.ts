// #2155 — "How this works" help for the Settings & admin sidebar section.
//
// On every Settings & admin route, **?** opens that page's own module help
// (web/help/modules/<id>.md) with its body, its key terms and a "Read more"
// link to the matching docs page, and the page header carries at least one
// of the module's terms as a <Term>.
//
// The **?** and the subtitle live in the page header, which renders
// whatever the page's own fetches return, so the spec needs no seed data.
// The signed-in test user is a super admin, so every /admin page opens.

import { expect, test } from "./fixtures";
import { clickHelp } from "./help";

const DOCS = "https://mgoodric.github.io/security-atlas/help/";

const ROUTES = [
  {
    route: "/settings",
    id: "settings",
    title: "Settings",
    body: "Settings holds your own preferences",
    term: "tenant",
  },
  {
    route: "/admin",
    id: "admin",
    title: "Admin",
    body: "An admin of a tenant opens the pages that configure their own tenant",
    term: "sso",
  },
  {
    route: "/admin/users",
    id: "admin-users",
    title: "Users",
    body: "A person's access is the roles they hold in a tenant",
    term: "role",
  },
  {
    route: "/admin/api-keys",
    id: "admin-api-keys",
    title: "API keys",
    body: "security-atlas shows the secret once",
    term: "api-key",
  },
  {
    route: "/admin/sso",
    id: "admin-sso",
    title: "SSO",
    body: "Single sign-on lets people sign in to security-atlas",
    term: "sso",
  },
  {
    route: "/admin/features",
    id: "admin-features",
    title: "Features",
    body: "security-atlas is made of modules",
    term: "module",
  },
  {
    route: "/admin/tenants",
    id: "admin-tenants",
    title: "Tenants",
    body: "Opting in to multi-tenant",
    term: "tenant",
  },
  {
    route: "/admin/super-admins",
    id: "admin-super-admins",
    title: "Super admins",
    body: "A super admin looks after the install as a whole",
    term: "super-admin",
  },
  {
    route: "/admin/demo",
    id: "admin-demo",
    title: "Demo tools",
    body: "The demo tools fill a separate tenant",
    term: "tenant",
  },
] as const;

test.describe("Settings & admin help (#2155)", () => {
  for (const r of ROUTES) {
    test(`${r.route} opens the ${r.id} help`, async ({ authedPage: page }) => {
      await page.goto(r.route);

      // The page header names at least one of the module's terms.
      await expect(
        page.getByTestId(`term-${r.term}-trigger`).first(),
      ).toBeVisible();

      const button = page.getByTestId("help-button");
      await expect(button).toHaveAccessibleName(`How this works: ${r.title}`);
      await clickHelp(button);

      const panel = page.getByTestId("help-panel");
      await expect(panel).toBeVisible();
      await expect(
        panel.getByRole("heading", { name: r.title, exact: true }),
      ).toBeVisible();
      await expect(page.getByTestId("help-panel-body")).toContainText(r.body);
      await expect(page.getByTestId("help-panel-terms")).not.toBeEmpty();
      await expect(page.getByTestId("help-panel-read-more")).toHaveAttribute(
        "href",
        `${DOCS}${r.id}/`,
      );

      await page.keyboard.press("Escape");
      await expect(panel).toHaveCount(0);
    });
  }
});
