// #2137 — opening a manual attestation's evidence row shows its statement
// and result on the evidence detail page.
//
// `fixtures/e2e/evidence-detail.sql` seeds one manual attestation in the
// demo tenant. The list BFF is route-mocked to return just that row, so
// the click does not depend on what else other fixtures put in the ledger.
// The detail read is NOT mocked: it runs through the real
// `/api/evidence/{id}` -> `GET /v1/evidence/{id}` path against the seeded
// row. Waits are web-first.
//
// Hard rule (P0-A9): neutral test strings only; no vendor-prefixed tokens.

import { expect, test } from "./fixtures";
import { DEMO_CONTROL_ID, seedFromFixture } from "./seed";

const EVIDENCE_ID = "66666666-6666-6666-6666-66666666d137";
const STATEMENT =
  "Evidence-detail fixture: quarterly console access reviewed, no exceptions.";

test.describe("evidence detail (#2137)", () => {
  test.beforeAll(() => {
    seedFromFixture("evidence-detail");
  });

  test("opening a manual attestation's row shows its statement and result", async ({
    authedPage: page,
  }) => {
    await page.route(
      (url) => url.pathname === "/api/evidence",
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            control_id: "",
            evidence: [
              {
                evidence_id: EVIDENCE_ID,
                evidence_kind: "manual.attestation.v1",
                observed_at: "2026-11-14T00:00:00Z",
                source: { source: "e2e-fixture" },
                content_hash: "sha256-evidence-detail-fixture-01",
                scope_cell: null,
                result: "pass",
              },
            ],
            count: 1,
            total: 1,
            next_cursor: "",
          }),
        });
      },
    );

    await page.goto("/evidence");
    const row = page.getByTestId("list-table-row").first();
    await expect(row).toBeVisible({ timeout: 30_000 });
    await row.click();

    await expect(page).toHaveURL(new RegExp(`/evidence/${EVIDENCE_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("evidence-detail-statement")).toHaveText(
      STATEMENT,
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("evidence-detail-result")).toHaveText("Pass");
    await expect(page.getByTestId("evidence-detail-attestor")).toHaveText(
      "demo-operator@example.invalid",
    );
    const link = page
      .getByTestId("evidence-detail-supporting")
      .getByRole("link");
    await expect(link).toHaveAttribute(
      "href",
      "https://example.invalid/reviews/q3",
    );
    await expect(link).toHaveAttribute("rel", "noopener noreferrer");
    await expect(
      page.getByTestId("evidence-detail-control").getByRole("link"),
    ).toHaveAttribute("href", `/controls/${DEMO_CONTROL_ID}`);
  });

  test("an unknown evidence id renders the in-shell not-found page", async ({
    authedPage: page,
  }) => {
    await page.goto("/evidence/00000000-0000-4000-8000-000000002137");
    await expect(page.getByTestId("authed-not-found")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("evidence-detail-statement")).toHaveCount(0);
  });
});
