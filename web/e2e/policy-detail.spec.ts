// Slice 672 — Playwright E2E for the read-only policy detail route +
// the in-shell authed 404 boundary.
//
// Closes ATLAS-024: policy titles in /policies linked to /policies/{id}
// which did not exist, so every click was a hard SHELL-LESS Next 404.
// This spec pins the two load-bearing outcomes:
//
//   1. From /policies, clicking a policy title navigates to a 200
//      detail page that renders the policy (title + markdown body).
//   2. Navigating to /policies/<nonexistent-uuid> renders the in-shell
//      not-found boundary (sidebar/nav still present) — NOT a stranded
//      full-page 404.
//
// Hermetic mock discipline (feedback_e2e_shared_db_hermetic_mock,
// slice 594): the assertions DO NOT depend on shared-DB seed state.
// Both the list BFF (`/api/policies`) and the detail BFF
// (`/api/policies/{id}`) are route-mocked so the spec is deterministic
// regardless of what the docker-compose bring-up seeded. The policies
// endpoints have no contract golden yet (#410/#411 track them), so the
// mock bodies are hand-written to the production wire shape (the BFF
// list response `{ policies }`, the detail response `{ policy, ack_rate }`).

import { expect, test } from "./fixtures";

const POLICY_ID = "00000000-0000-0000-0000-000000000672";
const MISSING_ID = "00000000-0000-0000-0000-0000000006ff";

const LIST_BODY = {
  policies: [
    {
      id: POLICY_ID,
      title: "Information Security Policy",
      version: "v3.2",
      body_md: "",
      owner_role: "security_lead",
      approver_role: "cto",
      linked_control_ids: [],
      acknowledgment_required_roles: ["all_staff"],
      status: "published",
      source_attribution: "in_house",
      created_by: "user-1",
      published_at: "2026-01-15T00:00:00Z",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-04-22T00:00:00Z",
      ack_rate: { numerator: 8, denominator: 10, percent: 80 },
    },
  ],
};

const DETAIL_BODY = {
  policy: {
    id: POLICY_ID,
    title: "Information Security Policy",
    version: "v3.2",
    body_md:
      "# Purpose\n\nThis policy defines **information security** controls.\n\n- Access is least-privilege.\n- MFA is required.\n",
    owner_role: "security_lead",
    approver_role: "cto",
    linked_control_ids: [],
    acknowledgment_required_roles: ["all_staff"],
    status: "published",
    source_attribution: "in_house",
    created_by: "user-1",
    effective_date: "2026-01-15",
    published_at: "2026-01-15T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-04-22T00:00:00Z",
  },
  ack_rate: {
    numerator: 8,
    denominator: 10,
    percent: 80,
    window_seconds: 31536000,
  },
};

test.describe("policy detail route (slice 672)", () => {
  test.beforeEach(async ({ authedPage: page }) => {
    // List BFF — drives the /policies table the spec clicks from.
    await page.route("**/api/policies", (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(LIST_BODY),
      });
    });
    // Detail BFF — 200 for the seeded id, 404 for the missing id.
    await page.route(`**/api/policies/${POLICY_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(DETAIL_BODY),
      }),
    );
    await page.route(`**/api/policies/${MISSING_ID}`, (route) =>
      route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "policy not found" }),
      }),
    );
  });

  test("AC-2/AC-4: clicking a policy title lands on a 200 detail page that renders title + body", async ({
    authedPage: page,
  }) => {
    await page.goto("/policies");
    // The list row title links to /policies/{id}; click it.
    const title = page.getByTestId("policies-row-title").first();
    await expect(title).toBeVisible();

    const detailResp = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/policies/${POLICY_ID}`) && r.status() === 200,
      { timeout: 30_000 },
    );
    await title.click();
    await detailResp;

    // Landed on the detail route.
    await expect(page).toHaveURL(new RegExp(`/policies/${POLICY_ID}$`));

    // The detail page renders the policy header + markdown body.
    await expect(page.getByTestId("policy-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("policy-detail-title")).toHaveText(
      "Information Security Policy",
    );
    await expect(page.getByTestId("policy-detail-status")).toContainText(
      "published",
    );
    // body_md rendered as markdown: the `# Purpose` heading becomes an
    // <h1>, and the **bold** span becomes <strong>.
    const body = page.getByTestId("policy-detail-body");
    await expect(body).toBeVisible();
    await expect(body.locator("h1")).toContainText("Purpose");
    await expect(body.locator("strong")).toContainText("information security");
    await expect(body.locator("li").first()).toContainText("least-privilege");

    // The acknowledgment rate cell renders for the published policy.
    await expect(page.getByTestId("policy-detail-ack-rate")).toContainText(
      "80%",
    );

    // The PDF link is present (the one outbound action; read-only page).
    await expect(page.getByTestId("policy-detail-pdf-link")).toBeVisible();
  });

  test("AC-3: a missing policy id renders the in-shell not-found, NOT a stranded full-page 404", async ({
    authedPage: page,
  }) => {
    await page.goto(`/policies/${MISSING_ID}`);

    // The in-shell not-found boundary renders.
    await expect(page.getByTestId("authed-not-found")).toBeVisible({
      timeout: 30_000,
    });

    // The app shell (desktop sidebar nav) is STILL present — recovery
    // does not require the browser back button. This is the load-bearing
    // AC-3 assertion: the boundary lives inside the (authed) layout.
    await expect(page.getByTestId("sidebar-desktop")).toBeVisible();

    // The not-found CTA navigates back into the app.
    await expect(page.getByTestId("authed-not-found-cta")).toBeVisible();

    // Defensive: the framework's default shell-less 404 copy is absent.
    await expect(page.locator("body")).not.toContainText(
      "This page could not be found",
    );
  });
});
