// #2141 — the /controls list's Export menu and bulk actions (front door,
// #2072).
//
// The six export links sit in one Export menu, each at the URL it had as
// a header button. Selected controls take four bulk actions: assign owner
// (to anyone, by picker), evaluate now, link to an action plan and
// request exception. The selection cap is mentioned only when a selection
// exceeds it.
//
// Two halves:
//   - Against the real platform, in a tenant of the spec's own
//     (fixtures/e2e/controls-bulk.sql), so its writes never race the
//     controls-list spec over the demo tenant's controls: the Export menu,
//     and bulk assign owner to another member, evaluate now and link to a
//     plan, each on three selected controls. #2780: the fixture only
//     inserts and the link test makes a plan of its own, because both
//     halves apply the fixture from workers that run side by side.
//   - Route-mocked over the same sign-in (the slice 594 convention) for
//     what the seed cannot cheaply hold: more controls than the cap, a
//     viewer, and a batch the platform partly refuses.
// The module-off case needs the server-side /v1/me/modules read and lives
// in module-links.spec.ts.
//
// Hard rule (P0-A9 / GitGuardian): every id and string is neutral.

import type { Page, Route } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002141";
const ADMIN = "44444444-4444-4444-4444-444444442141";
const CONTROL_TITLES = [
  "Bulk e2e control one",
  "Bulk e2e control two",
  "Bulk e2e control three",
];
const CONTROL_IDS = [
  "88888888-8888-8888-8888-888888882141",
  "88888888-8888-8888-8888-888888882142",
  "88888888-8888-8888-8888-888888882143",
];

async function signInAsAdmin(page: Page): Promise<void> {
  const url = `${
    process.env.ATLAS_HTTP_URL ?? "http://localhost:8080"
  }/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: TENANT,
      user_id: ADMIN,
      roles: ["admin"],
      super_admin: false,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2141: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const { token } = (await res.json()) as { token?: string };
  if (!token) throw new Error(`#2141: ${url} returned no token`);
  const web = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  await page.context().clearCookies();
  await page.context().addCookies([
    {
      name: ATLAS_JWT_COOKIE,
      value: token,
      domain: web.hostname,
      path: "/",
      httpOnly: true,
      secure: web.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

function row(page: Page, title: string) {
  return page.getByTestId("list-table-row").filter({ hasText: title });
}

async function selectThree(page: Page): Promise<void> {
  await page.goto("/controls");
  for (const title of CONTROL_TITLES) {
    await expect(row(page, title)).toBeVisible({ timeout: 30_000 });
    await row(page, title).getByTestId("controls-row-select").click();
  }
  await expect(page.getByTestId("controls-selection-count")).toHaveText(
    "3 selected",
  );
}

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

test.describe("#2141 — Export menu and bulk actions (real platform)", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(() => {
    seedFromFixture("controls-bulk");
  });

  test.beforeEach(async ({ page }) => {
    await signInAsAdmin(page);
  });

  test("Export offers every previous format from one menu, at the same URLs", async ({
    page,
  }) => {
    await page.goto("/controls");
    await expect(page.getByTestId("controls-export-menu")).toBeVisible({
      timeout: 30_000,
    });
    // No export button sits in the header any more.
    await expect(page.getByTestId("controls-export-csv")).toHaveCount(0);

    await page.getByTestId("controls-export-menu").click();
    const expected: Record<string, string> = {
      "controls-export-csv": "/api/controls/export?format=csv",
      "controls-export-json": "/api/controls/export?format=json",
      "controls-export-xlsx": "/api/controls/export?format=xlsx",
      "controls-history-export-csv": "/api/controls/history/export?format=csv",
      "controls-history-export-json":
        "/api/controls/history/export?format=json",
      "controls-history-export-xlsx":
        "/api/controls/history/export?format=xlsx",
    };
    for (const [testId, href] of Object.entries(expected)) {
      const item = page.getByTestId(testId);
      await expect(item).toBeVisible();
      await expect(item).toHaveAttribute("href", href);
      await expect(item).toHaveAttribute("download", "");
    }
    // The link still downloads.
    const res = await page.request.get("/api/controls/export?format=csv");
    expect(res.ok()).toBe(true);
  });

  test("bulk assign owner hands three controls to another member", async ({
    page,
  }) => {
    await selectThree(page);
    // Below the cap, the cap is not mentioned.
    await expect(page.getByTestId("controls-selection-overcap")).toHaveCount(0);
    await expect(page.getByTestId("controls-selection-bar")).not.toContainText(
      /cap/i,
    );

    await page.getByTestId("controls-bulk-assign-owner").click();
    await page.getByTestId("controls-bulk-owner").fill("Bulk Owner");
    await page
      .getByTestId("controls-bulk-owner-option")
      .filter({ has: page.getByText("Bulk Owner E2E", { exact: true }) })
      .click();
    await page.getByTestId("controls-bulk-assign-confirm").click();

    await expect(
      page.getByTestId("controls-bulk-assign-message"),
    ).toContainText("Assigned 3 controls to the new owner.");
    await expect(page.getByTestId("controls-selection-bar")).toHaveCount(0);
    for (const title of CONTROL_TITLES) {
      await expect(
        row(page, title).getByTestId("controls-row-owner"),
      ).toContainText("Bulk Owner E2E");
    }
  });

  test("bulk evaluate now evaluates three controls", async ({ page }) => {
    await selectThree(page);
    const evaluated: string[] = [];
    page.on("response", (r) => {
      const m = /\/api\/controls\/([^/]+)\/evaluate$/.exec(
        new URL(r.url()).pathname,
      );
      if (m && r.request().method() === "POST" && r.ok()) evaluated.push(m[1]);
    });
    await page.getByTestId("controls-bulk-evaluate").click();
    await expect(page.getByTestId("controls-bulk-result-summary")).toHaveText(
      "3 controls evaluated.",
    );
    expect(evaluated.sort()).toEqual([...CONTROL_IDS].sort());
    await expect(page.getByTestId("controls-selection-bar")).toHaveCount(0);
  });

  test("bulk link to an action plan links three controls", async ({
    page,
  }, testInfo) => {
    // #2780 — a plan of this run's own, so no other run's writes or
    // fixture apply can change its links between the link and the read.
    const title = `Bulk e2e plan ${testInfo.repeatEachIndex}-${
      testInfo.retry
    }-${Date.now()}`;
    const created = await page.request.post("/api/action-plans", {
      data: { title, owner_id: ADMIN, due_date: null },
    });
    expect(created.ok(), await created.text()).toBe(true);
    const plan = ((await created.json()) as { action_plan: { id: string } })
      .action_plan.id;

    await selectThree(page);
    await page.getByTestId("controls-bulk-link-plan").click();
    await page.getByTestId("controls-bulk-plan-search").fill(title);
    const select = page.getByTestId("controls-bulk-plan-select");
    await expect(select.locator(`option[value="${plan}"]`)).toHaveCount(1);
    await select.selectOption(plan);
    await page.getByTestId("controls-bulk-link-confirm").click();
    await expect(page.getByTestId("controls-bulk-result-summary")).toHaveText(
      "3 controls linked to the plan.",
    );

    const res = await page.request.get(`/api/action-plans/${plan}`);
    expect(res.ok()).toBe(true);
    const body = (await res.json()) as {
      linkage: { controls: { target_id: string }[] };
    };
    expect(body.linkage.controls.map((c) => c.target_id).sort()).toEqual(
      [...CONTROL_IDS].sort(),
    );
  });
});

test.describe("#2141 — bulk actions (route-mocked)", () => {
  test.beforeAll(() => {
    seedFromFixture("controls-bulk");
  });

  test.beforeEach(async ({ page }) => {
    await signInAsAdmin(page);
  });

  test("a selection over the cap shows the limit and disables every action", async ({
    page,
  }) => {
    const many = Array.from({ length: 201 }, (_, i) => ({
      id: `00000000-0000-4000-8000-${String(i).padStart(12, "0")}`,
      scf_id: "",
      title: `Many control ${i}`,
      control_family: "IAC",
      implementation_type: "automated",
      lifecycle_state: "active",
      scf_anchor_id: null,
      state: null,
      frameworks: [],
      anchor_fit: "none",
      owner_user_id: null,
    }));
    await page.route(/\/api\/controls-list\?.*include=state/, (route: Route) =>
      route.fulfill(json({ controls: many, count: 201, total: 201 })),
    );
    await page.goto("/controls");
    await expect(row(page, "Many control 200")).toBeVisible({
      timeout: 30_000,
    });
    // The table's header checkbox; each row card repeats the header.
    await page
      .getByTestId("list-table-wrap")
      .getByTestId("controls-select-all")
      .click();
    await expect(page.getByTestId("controls-selection-count")).toHaveText(
      "201 selected",
    );
    await expect(page.getByTestId("controls-selection-overcap")).toContainText(
      "at most 200 controls",
    );
    await expect(page.getByTestId("controls-bulk-assign-owner")).toBeDisabled();
    await expect(page.getByTestId("controls-bulk-evaluate")).toBeDisabled();
    await expect(page.getByTestId("controls-bulk-link-plan")).toBeDisabled();
    await expect(
      page.getByTestId("controls-bulk-request-exception"),
    ).toBeDisabled();

    // Back below the cap, the message goes.
    await row(page, "Many control 0")
      .getByTestId("controls-row-select")
      .click();
    await expect(page.getByTestId("controls-selection-count")).toHaveText(
      "200 selected",
    );
    await expect(page.getByTestId("controls-selection-overcap")).toHaveCount(0);
    await expect(page.getByTestId("controls-bulk-evaluate")).toBeEnabled();
  });

  test("a viewer is offered no bulk action and no selection", async ({
    page,
  }) => {
    await page.route("**/api/me", (route: Route) =>
      route.fulfill(
        json({
          user_id: ADMIN,
          tenant_id: TENANT,
          display_name: "Vera Viewer",
          email: "viewer@example.test",
          idp_subject: "",
          tenant_role: "viewer",
          time_zone: null,
          theme: null,
          is_admin: false,
          is_tenant_admin: false,
          is_approver: false,
          has_tenant_role: true,
          owner_roles: [],
          roles: ["viewer"],
        }),
      ),
    );
    await page.goto("/controls");
    await expect(row(page, CONTROL_TITLES[0])).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("controls-row-select")).toHaveCount(0);
    await expect(page.getByTestId("controls-select-all")).toHaveCount(0);
    await expect(page.getByTestId("controls-selection-bar")).toHaveCount(0);
    // Export stays: it is a read.
    await expect(page.getByTestId("controls-export-menu")).toBeVisible();
  });

  test("a batch the platform partly refuses names each refused control", async ({
    page,
  }) => {
    await page.route(/\/api\/controls\/[^/]+\/evaluate$/, (route: Route) => {
      const id = new URL(route.request().url()).pathname.split("/")[3];
      return id === CONTROL_IDS[1]
        ? route.fulfill(json({ error: "not the control's owner" }, 403))
        : route.fulfill(json({ control_id: id, states: [] }));
    });
    await selectThree(page);
    await page.getByTestId("controls-bulk-evaluate").click();

    const result = page.getByTestId("controls-bulk-assign-message");
    await expect(page.getByTestId("controls-bulk-result-summary")).toHaveText(
      "2 controls evaluated, 1 refused.",
    );
    await expect(page.getByTestId("controls-bulk-refusal")).toHaveCount(1);
    await expect(page.getByTestId("controls-bulk-refusal")).toContainText(
      "Bulk e2e control two",
    );
    await expect(result).toContainText("not the control's owner");
    // The refused control stays selected for a retry.
    await expect(page.getByTestId("controls-selection-count")).toHaveText(
      "1 selected",
    );
  });
});
