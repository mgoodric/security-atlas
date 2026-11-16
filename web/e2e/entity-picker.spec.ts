// #2086 — Playwright: the entity picker replaces the four UUID fields.
//
//   - New audit period: pick the framework version by name.
//   - New action plan: pick the owner by name.
//   - Admin › Super admins: grant by picking a user.
//   - Activity: filter by picking the actor.
//   - A pasted valid ID is accepted; an invalid one shows an inline error.
//   - Keyboard-only selection works, with a visible focus ring and no
//     serious or critical axe violations while the list is open.
//
// Mocked with `route.fulfill` (the questionnaires.spec.ts convention): the
// assertions are on the request each form sends, which must carry the
// picked entity's ID.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import AxeBuilder from "@axe-core/playwright";
import type { Locator, Page, Route } from "@playwright/test";

import { expectNoSeriousAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";

const FV_ISO = "fafafafa-0000-4000-8000-000000002086";
const FV_SOC2 = "fbfbfbfb-0000-4000-8000-000000002086";
const USER_ALICE = "a1a1a1a1-0000-4000-8000-000000002086";
const USER_BOB = "b2b2b2b2-0000-4000-8000-000000002086";
const KEY_ALICE = "c3c3c3c3-0000-4000-8000-000000002086";
const PLAN_ID = "d4d4d4d4-0000-4000-8000-000000002086";
const PERIOD_ID = "e5e5e5e5-0000-4000-8000-000000002086";

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

async function mockDirectories(page: Page) {
  await page.route(
    (url) => url.pathname === "/api/frameworks",
    (route) =>
      json(route, 200, {
        frameworks: [
          {
            id: "f-iso",
            name: "ISO/IEC 27001",
            slug: "iso27001",
            versions: [{ id: FV_ISO, version: "2022", status: "current" }],
          },
          {
            id: "f-soc2",
            name: "SOC 2",
            slug: "soc2",
            versions: [{ id: FV_SOC2, version: "2017", status: "current" }],
          },
        ],
      }),
  );
  await page.route("**/api/people", (route) =>
    json(route, 200, {
      users: [
        { id: USER_ALICE, display_name: "Alice Picker", status: "active" },
        { id: USER_BOB, display_name: "Bob Picker", status: "active" },
      ],
      api_keys: [{ id: KEY_ALICE, owner_id: USER_ALICE }],
    }),
  );
}

// pasteInto fires a real paste event carrying text, the way a browser
// does for Ctrl/Cmd+V, without depending on the OS clipboard.
async function pasteInto(input: Locator, text: string) {
  await input.focus();
  await input.evaluate((el, t) => {
    const data = new DataTransfer();
    data.setData("text/plain", t);
    el.dispatchEvent(
      new ClipboardEvent("paste", {
        clipboardData: data,
        bubbles: true,
        cancelable: true,
      }),
    );
  }, text);
}

async function pick(page: Page, testId: string, query: string, name: string) {
  await page.getByTestId(testId).fill(query);
  await page
    .getByTestId(`${testId}-option`)
    .filter({ has: page.getByText(name, { exact: true }) })
    .click();
  await expect(page.getByTestId(testId)).toHaveValue(name);
}

function mockAuditCreate(page: Page): Promise<Record<string, unknown>> {
  return new Promise((resolve) => {
    void page.route("**/api/audits", (route, req) => {
      if (req.method() === "POST") {
        resolve(req.postDataJSON() as Record<string, unknown>);
        return json(route, 201, {
          id: PERIOD_ID,
          name: "Picker period",
          status: "open",
        });
      }
      return json(route, 200, { audit_periods: [], count: 0 });
    });
  });
}

async function fillAuditDates(page: Page) {
  await page.getByTestId("audits-create-name").fill("Picker period");
  await page.getByTestId("audits-create-period-start").fill("2027-01-01");
  await page.getByTestId("audits-create-period-end").fill("2027-03-31");
}

test.beforeEach(async ({ authedPage: page }) => {
  await mockDirectories(page);
});

test("creates an audit period by picking the framework by name", async ({
  authedPage: page,
}) => {
  const posted = mockAuditCreate(page);
  await page.goto("/audits/new");
  await pick(
    page,
    "audits-create-framework-version-id",
    "27001",
    "ISO/IEC 27001:2022",
  );
  await fillAuditDates(page);
  await page.getByTestId("audits-create-submit").click();
  expect((await posted).framework_version_id).toBe(FV_ISO);
  await expect(page).toHaveURL(/\/audits(\?|$)/);
});

test("creates an action plan by picking the owner by name", async ({
  authedPage: page,
}) => {
  let body: Record<string, unknown> | null = null;
  await page.route("**/api/action-plans", (route, req) => {
    if (req.method() === "POST") {
      body = req.postDataJSON() as Record<string, unknown>;
      return json(route, 201, {
        action_plan: { id: PLAN_ID, title: "Picker plan", status: "open" },
      });
    }
    return json(route, 200, { action_plans: [], count: 0 });
  });
  await page.route(`**/api/action-plans/${PLAN_ID}`, (route) =>
    json(route, 200, {
      action_plan: {
        id: PLAN_ID,
        title: "Picker plan",
        owner_id: USER_BOB,
        status: "open",
        created_at: "2026-11-16T00:00:00Z",
        updated_at: "2026-11-16T00:00:00Z",
      },
      linkage: { risks: [], controls: [] },
    }),
  );

  await page.goto("/action-plans/new");
  await page.getByTestId("action-plan-title").fill("Picker plan");
  await pick(page, "action-plan-owner", "bob", "Bob Picker");
  await page.getByTestId("action-plan-submit").click();
  await expect(page).toHaveURL(new RegExp(`/action-plans/${PLAN_ID}$`));
  expect(body).not.toBeNull();
  expect(body!.owner_id).toBe(USER_BOB);
});

test("grants super admin by picking a user", async ({ authedPage: page }) => {
  let granted: string | undefined;
  await page.route("**/api/admin/super-admins", (route, req) => {
    if (req.method() === "POST") {
      granted = (req.postDataJSON() as { user_id?: string }).user_id;
      return json(route, 200, {
        user_id: granted,
        display_name: "Bob Picker",
        granted_at: "2026-11-16T00:00:00Z",
        granted_via: "manual_grant",
      });
    }
    return json(route, 200, { items: [] });
  });

  await page.goto("/admin/super-admins");
  await pick(page, "grant-user-id-input", "Bob", "Bob Picker");
  await page.getByTestId("grant-super-admin-submit").click();
  await expect.poll(() => granted).toBe(USER_BOB);
});

test("filters Activity by picking the actor", async ({ authedPage: page }) => {
  const actors: string[] = [];
  await page.route("**/api/activity**", (route, req) => {
    const actor = new URL(req.url()).searchParams.get("actor");
    if (actor) actors.push(actor);
    return json(route, 200, { entries: [] });
  });

  await page.goto("/activity");
  await pick(page, "activity-actor", "alice", "Alice Picker");
  await expect(page).toHaveURL(new RegExp(`actor=${USER_ALICE}`));
  await expect.poll(() => actors).toContain(USER_ALICE);

  // API keys are listed by owner too.
  await pick(
    page,
    "activity-actor",
    "API key",
    "API key (owner: Alice Picker)",
  );
  await expect(page).toHaveURL(new RegExp(`actor=${KEY_ALICE}`));
});

test("a pasted valid ID is selected; an invalid one shows an inline error", async ({
  authedPage: page,
}) => {
  const posted = mockAuditCreate(page);
  await page.goto("/audits/new");
  const input = page.getByTestId("audits-create-framework-version-id");
  const error = page.getByTestId("audits-create-framework-version-id-error");

  await pasteInto(input, "not-a-version-id");
  await expect(error).toContainText(
    '"not-a-version-id" is not a valid framework version ID',
  );
  await expect(input).toHaveAttribute("aria-invalid", "true");
  await expect(input).toHaveAttribute(
    "aria-describedby",
    /audits-create-framework-version-id-error/,
  );

  // A valid ID resolves to the listed version's label.
  await input.fill("");
  await pasteInto(input, FV_SOC2.toUpperCase());
  await expect(input).toHaveValue("SOC 2 2017");
  await expect(error).toHaveCount(0);

  await fillAuditDates(page);
  await page.getByTestId("audits-create-submit").click();
  expect((await posted).framework_version_id).toBe(FV_SOC2);
});

test("keyboard-only selection, with a focus ring and no serious axe findings", async ({
  authedPage: page,
}) => {
  await page.goto("/action-plans/new");
  await page.getByTestId("action-plan-title").focus();
  await page.keyboard.press("Tab");
  const input = page.getByTestId("action-plan-owner");
  await expect(input).toBeFocused();

  // The focus indicator is the shared ring (WCAG 2.4.7).
  const ring = await input.evaluate((el) =>
    getComputedStyle(el).getPropertyValue("box-shadow"),
  );
  expect(ring).not.toBe("none");

  expectNoSeriousAxe(await seriousAxeViolations(page), "entity picker idle");
  // Typing opens the list with the first match (Alice) highlighted.
  await page.keyboard.type("Picker");
  await expect(page.getByRole("listbox")).toBeVisible();
  await expect(
    page.getByRole("option", { name: /Alice Picker/ }),
  ).toBeVisible();
  // While the list is open, base-ui (floating-ui's markOthers) sets
  // aria-hidden on everything outside the combobox so a screen reader
  // stays on it; axe's aria-hidden-focus reads that as hidden focusable
  // content. The open state is therefore checked on the widget itself,
  // and the whole page before opening and after closing.
  const open = await new AxeBuilder({ page })
    .include('[data-testid="action-plan-owner-picker"]')
    .include('[role="listbox"]')
    .analyze();
  expect(
    open.violations.filter(
      (v) => v.impact === "serious" || v.impact === "critical",
    ),
  ).toEqual([]);

  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(input).toHaveValue("Bob Picker");
  await expect(input).toBeFocused();
  await expect(page.getByRole("listbox")).toHaveCount(0);
  expectNoSeriousAxe(await seriousAxeViolations(page), "entity picker closed");
});
