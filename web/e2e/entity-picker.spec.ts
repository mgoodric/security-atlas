// #2086 — Playwright: the entity picker replaces the four UUID fields.
//
//   - New audit period: pick the framework version by name.
//   - New action plan: pick the owner by name.
//   - Admin › Super admins: grant by picking a user.
//   - Activity: filter by picking the actor.
//   - A pasted valid ID is accepted; an invalid one shows an inline error.
//   - Keyboard-only selection works, with a visible focus ring and no
//     serious or critical axe violations while the list is open.
//   - #2087: New action plan links risks and controls through the
//     multi-select, capped at 50; a refused list reads "not available".
//     The new-risk form's control picker is covered by
//     risks-create-control-link.spec.ts.
//   - #2432: the multi-select passes axe with its list open. The
//     evidence kind (the questionnaire cite field, its 403 path and its
//     raw-value scan) is covered by questionnaires.spec.ts.
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
  await page.route(/\/api\/people(\?.*)?$/, (route) =>
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

// #2087 — risks and controls come from the tenant's own lists.
const RISK_ACCESS = "a7a7a7a7-0000-4000-8000-000000002087";
const RISK_VENDOR = "b8b8b8b8-0000-4000-8000-000000002087";
const CONTROL_MFA = "c9c9c9c9-0000-4000-8000-000000002087";
const CONTROL_BACKUP = "dadadada-0000-4000-8000-000000002087";

function riskRow(id: string, title: string) {
  return {
    id,
    title,
    category: "operational",
    category_name: "Operational",
    treatment: "mitigate",
    status: "open",
  };
}

async function mockRisksAndControls(
  page: Page,
  risks: { status: number; body: unknown },
) {
  await page.route(
    (url) => url.pathname === "/api/risks",
    (route) => json(route, risks.status, risks.body),
  );
  await page.route(
    (url) => url.pathname === "/api/controls-list",
    (route) =>
      json(route, 200, {
        controls: [
          {
            id: CONTROL_MFA,
            title: "Multi-factor authentication",
            control_family: "IAC",
            scf_id: "IAC-06",
            lifecycle_state: "active",
            bundle_id: "soc2-starter",
          },
          {
            id: CONTROL_BACKUP,
            title: "Backup integrity verification",
            control_family: "BCD",
            scf_id: "BCD-11",
            lifecycle_state: "active",
            bundle_id: "soc2-starter",
          },
        ],
        count: 2,
      }),
  );
}

// mockActionPlanCreate records the create body and every link POST.
function mockActionPlanCreate(page: Page) {
  const seen = {
    body: null as Record<string, unknown> | null,
    links: [] as string[],
  };
  void page.route("**/api/action-plans", (route, req) => {
    if (req.method() === "POST") {
      seen.body = req.postDataJSON() as Record<string, unknown>;
      return json(route, 201, {
        action_plan: { id: PLAN_ID, title: "Linked plan", status: "open" },
      });
    }
    return json(route, 200, { action_plans: [], count: 0 });
  });
  void page.route(`**/api/action-plans/${PLAN_ID}/**`, (route, req) => {
    if (req.method() === "POST") {
      seen.links.push(new URL(req.url()).pathname);
      return json(route, 201, {});
    }
    return json(route, 200, { items: [] });
  });
  void page.route(`**/api/action-plans/${PLAN_ID}`, (route) =>
    json(route, 200, {
      action_plan: {
        id: PLAN_ID,
        title: "Linked plan",
        owner_id: USER_BOB,
        status: "open",
        created_at: "2026-11-16T00:00:00Z",
        updated_at: "2026-11-16T00:00:00Z",
      },
      linkage: { risks: [], controls: [] },
    }),
  );
  return seen;
}

test("New action plan links risks and controls through the shared picker", async ({
  authedPage: page,
}) => {
  await mockRisksAndControls(page, {
    status: 200,
    body: {
      risks: [
        riskRow(RISK_ACCESS, "Stale access reviews"),
        riskRow(RISK_VENDOR, "Unvetted vendor"),
      ],
    },
  });
  const seen = mockActionPlanCreate(page);

  await page.goto("/action-plans/new");
  await page.getByTestId("action-plan-title").fill("Linked plan");
  await pick(page, "action-plan-owner", "bob", "Bob Picker");

  const risks = page.getByTestId("action-plan-risks");
  const controls = page.getByTestId("action-plan-controls");
  await expect(risks).toHaveAttribute("data-entity-picker", "risk");
  await expect(controls).toHaveAttribute("data-entity-picker", "control");
  await expect(risks.locator("legend")).toContainText("(0/50)");

  // Search narrows the risk list; a checkbox picks the row.
  await page.getByTestId("action-plan-risks-search").fill("vendor");
  await expect(
    page.getByTestId(`action-plan-risks-option-${RISK_VENDOR}`),
  ).toBeVisible();
  await expect(
    page.getByTestId(`action-plan-risks-option-${RISK_ACCESS}`),
  ).toHaveCount(0);
  await page.getByTestId(`action-plan-risks-checkbox-${RISK_VENDOR}`).check();
  await expect(risks.locator("legend")).toContainText("(1/50)");

  // Controls search on the SCF code as well as the title.
  await page.getByTestId("action-plan-controls-search").fill("IAC-06");
  await page
    .getByTestId(`action-plan-controls-checkbox-${CONTROL_MFA}`)
    .check();
  await expect(controls.locator("legend")).toContainText("(1/50)");

  await page.getByTestId("action-plan-submit").click();
  await expect(page).toHaveURL(new RegExp(`/action-plans/${PLAN_ID}$`));
  expect(seen.body!.owner_id).toBe(USER_BOB);
  expect(seen.links).toEqual([
    `/api/action-plans/${PLAN_ID}/risks/${RISK_VENDOR}`,
    `/api/action-plans/${PLAN_ID}/controls/${CONTROL_MFA}`,
  ]);
});

test("the multi-picker has no serious axe findings with its list open (#2432)", async ({
  authedPage: page,
}) => {
  await mockRisksAndControls(page, {
    status: 200,
    body: {
      risks: [
        riskRow(RISK_ACCESS, "Stale access reviews"),
        riskRow(RISK_VENDOR, "Unvetted vendor"),
      ],
    },
  });
  await page.goto("/action-plans/new");

  // Open: the list rendered, narrowed by a search, one row checked.
  await page.getByTestId("action-plan-risks-search").fill("vendor");
  await page.getByTestId(`action-plan-risks-checkbox-${RISK_VENDOR}`).check();
  await expect(page.getByTestId("action-plan-risks-list")).toBeVisible();
  await expect(page.getByTestId("action-plan-controls-list")).toBeVisible();

  const open = await new AxeBuilder({ page })
    .include('[data-testid="action-plan-risks"]')
    .include('[data-testid="action-plan-controls"]')
    .analyze();
  expect(
    open.violations.filter(
      (v) => v.impact === "serious" || v.impact === "critical",
    ),
  ).toEqual([]);
  expectNoSeriousAxe(
    await seriousAxeViolations(page),
    "entity multi-picker open",
  );
});

test("the risk picker stops at 50 links, as the server does", async ({
  authedPage: page,
}) => {
  const ids = Array.from(
    { length: 51 },
    (_, i) => `eeeeeeee-0000-4000-8000-${String(i).padStart(12, "0")}`,
  );
  await mockRisksAndControls(page, {
    status: 200,
    body: { risks: ids.map((id, i) => riskRow(id, `Capped risk ${i}`)) },
  });

  await page.goto("/action-plans/new");
  const risks = page.getByTestId("action-plan-risks");
  await expect(page.getByTestId("action-plan-risks-list")).toBeVisible();
  for (const id of ids.slice(0, 50)) {
    await page.getByTestId(`action-plan-risks-checkbox-${id}`).check();
  }
  await expect(risks.locator("legend")).toContainText("(50/50)");
  await expect(page.getByTestId("action-plan-risks-cap")).toBeVisible();
  await expect(
    page.getByTestId(`action-plan-risks-checkbox-${ids[50]}`),
  ).toBeDisabled();

  // Unchecking one frees the slot again.
  await page.getByTestId(`action-plan-risks-checkbox-${ids[0]}`).uncheck();
  await expect(risks.locator("legend")).toContainText("(49/50)");
  await expect(
    page.getByTestId(`action-plan-risks-checkbox-${ids[50]}`),
  ).toBeEnabled();
});

test("a risk list the caller cannot read shows not available, not an error", async ({
  authedPage: page,
}) => {
  // 404 is what the module gate answers when Risk is switched off.
  await mockRisksAndControls(page, {
    status: 404,
    body: { error: "not found" },
  });

  await page.goto("/action-plans/new");
  await expect(page.getByTestId("action-plan-risks-unavailable")).toHaveText(
    "Risks are not available to you.",
  );
  await expect(page.getByTestId("action-plan-risks-error")).toHaveCount(0);
  // The other picker on the form is unaffected.
  await expect(page.getByTestId("action-plan-controls-list")).toBeVisible();
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

// #2414: machine and system actors in the window are listed by kind, and
// a deep-linked one keeps its value through a blur.
test("Activity lists machine actors and keeps a deep-linked one on blur", async ({
  authedPage: page,
}) => {
  const actors: string[] = [];
  await page.route("**/api/activity**", (route, req) => {
    const actor = new URL(req.url()).searchParams.get("actor");
    if (actor) actors.push(actor);
    return json(route, 200, { entries: [] });
  });
  await page.route("**/api/activity/actors**", (route) =>
    json(route, 200, { actors: ["oauth_client:ci", "system:scheduler"] }),
  );

  await page.goto("/activity?actor=system");
  const input = page.getByTestId("activity-actor");
  await expect(input).toHaveValue("System");
  await input.focus();
  await input.blur();
  await expect(page).toHaveURL(/actor=system(&|$)/);
  await expect(input).toHaveValue("System");

  await input.fill("ci");
  await page
    .getByTestId("activity-actor-option")
    .filter({ has: page.getByText("OAuth client", { exact: true }) })
    .click();
  await expect(page).toHaveURL(/actor=oauth_client%3Aci/);
  await expect.poll(() => actors).toContain("oauth_client:ci");
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
