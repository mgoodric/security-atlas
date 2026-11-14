// #1747 — Playwright E2E for imported policies.
//
// An imported policy is published with its historical lifecycle. This
// spec pins what an operator sees:
//
//   1. The policy list marks the imported policy "Imported" and flags it
//      "No linked controls" (imports may be published without controls).
//   2. The detail page shows "Imported" with the historical approver,
//      approval date and source link.
//   3. A tenant-authored policy shows none of that.
//
// Hermetic mock discipline (slice 594): both BFFs are route-mocked with
// bodies hand-written to the production wire shape (`wireFromPolicy` in
// internal/api/policies/handlers.go), so the assertions do not depend on
// shared-DB seed state.

import { expect, test } from "./fixtures";

const IMPORTED_ID = "00000000-0000-0000-0000-000000001747";
const AUTHORED_ID = "00000000-0000-0000-0000-000000001748";
const SOURCE_URL = "https://wiki.example.com/policies/isp";

const IMPORTED = {
  id: IMPORTED_ID,
  title: "Information Security Policy",
  version: "3.2",
  body_md: "# Purpose\n\nProtect information.\n",
  owner_role: "security_lead",
  approver_role: "unassigned",
  linked_control_ids: [],
  acknowledgment_required_roles: [],
  status: "published",
  source_attribution: "imported",
  created_by: "user-admin",
  effective_date: "2025-03-15",
  published_at: "2026-11-13T00:00:00Z",
  published_by: "user-admin",
  next_review_at: "2026-03-01T00:00:00Z",
  imported: true,
  historical_approver_name: "Jane Doe",
  historical_approved_at: "2025-03-01",
  source_url: SOURCE_URL,
  imported_by: "user-admin",
  imported_at: "2026-11-13T00:00:00Z",
  created_at: "2026-11-13T00:00:00Z",
  updated_at: "2026-11-13T00:00:00Z",
  warnings: ["orphan_policy"],
};

const AUTHORED = {
  id: AUTHORED_ID,
  title: "Access Control Policy",
  version: "1.0",
  body_md: "# Access\n\nLeast privilege.\n",
  owner_role: "security_lead",
  approver_role: "cto",
  linked_control_ids: ["00000000-0000-0000-0000-00000000c001"],
  acknowledgment_required_roles: [],
  status: "published",
  source_attribution: "tenant_authored",
  created_by: "user-1",
  effective_date: "2026-01-15",
  published_at: "2026-01-15T00:00:00Z",
  imported: false,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-15T00:00:00Z",
};

test.describe("imported policies (#1747)", () => {
  test.beforeEach(async ({ authedPage: page }) => {
    await page.route("**/api/policies", (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          policies: [
            { ...IMPORTED, ack_rate: null },
            { ...AUTHORED, ack_rate: null },
          ],
        }),
      });
    });
    for (const p of [IMPORTED, AUTHORED]) {
      await page.route(`**/api/policies/${p.id}`, (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ policy: p, ack_rate: null }),
        }),
      );
    }
  });

  test("the list marks the imported policy and flags its missing controls", async ({
    authedPage: page,
  }) => {
    await page.goto("/policies");
    const rows = page.locator("tr", {
      has: page.getByTestId("policies-row-title"),
    });
    const imported = rows.filter({ hasText: IMPORTED.title });
    const authored = rows.filter({ hasText: AUTHORED.title });
    await expect(imported).toHaveCount(1, { timeout: 30_000 });

    await expect(imported.getByTestId("policies-row-imported")).toHaveText(
      "Imported",
    );
    await expect(
      imported.getByTestId("policies-row-no-linked-controls"),
    ).toHaveText("No linked controls");
    await expect(authored.getByTestId("policies-row-imported")).toHaveCount(0);
    await expect(
      authored.getByTestId("policies-row-no-linked-controls"),
    ).toHaveCount(0);
  });

  test("the detail page shows Imported with the historical approver, date and source link", async ({
    authedPage: page,
  }) => {
    await page.goto(`/policies/${IMPORTED_ID}`);
    await expect(page.getByTestId("policy-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("policy-detail-imported")).toHaveText(
      "Imported",
    );
    await expect(
      page.getByTestId("policy-detail-historical-approver"),
    ).toHaveText("Jane Doe");
    await expect(
      page.getByTestId("policy-detail-historical-approved-at"),
    ).toHaveText("2025-03-01");
    await expect(page.getByTestId("policy-detail-next-review")).toHaveText(
      "2026-03-01",
    );
    const link = page.getByTestId("policy-detail-source-link");
    await expect(link).toHaveAttribute("href", SOURCE_URL);
    await expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  test("a tenant-authored policy shows no import history", async ({
    authedPage: page,
  }) => {
    await page.goto(`/policies/${AUTHORED_ID}`);
    await expect(page.getByTestId("policy-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("policy-detail-imported")).toHaveCount(0);
    await expect(page.getByTestId("policy-detail-import-history")).toHaveCount(
      0,
    );
  });
});
