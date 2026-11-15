// #2089 — attesting returns to the control with a toast, and the attest
// form is not left filled.
//
// The attest page is a server component, so its form metadata comes from
// the real platform: `fixtures/e2e/attest.sql` seeds one manual_attested
// control (the base seed's control is automated, which the attest-form
// route rejects). The browser-side submit is route-mocked, so the spec
// does not depend on the ingest pipeline. Waits are web-first.
//
// Hard rule (P0-A9): neutral test strings only; no vendor-prefixed
// tokens.

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const CONTROL_ID = "33333333-3333-3333-3333-33333333a001";

test.describe("attest returns to the control (#2089)", () => {
  test.beforeAll(() => {
    seedFromFixture("attest");
  });

  test("attesting returns to the control with a toast and leaves no filled form", async ({
    authedPage: page,
  }) => {
    let posted: { statement?: string } | null = null;
    await page.route(
      `**/api/controls/${CONTROL_ID}/attestations`,
      async (route, req) => {
        posted = JSON.parse(req.postData() ?? "{}") as { statement?: string };
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({
            record_id: "00000000-0000-4000-8000-00000000a2a9",
            hash: "sha256:test-hash",
            ingested_at: "2026-11-14T00:00:00Z",
            credential_id: "test-credential",
            deduplicated: false,
          }),
        });
      },
    );

    await page.goto(`/controls/${CONTROL_ID}/attest`);
    const statement = page.getByTestId("attest-statement");
    await expect(statement).toBeVisible({ timeout: 30_000 });

    // Empty statement: inline error beside the field, no submit.
    await page.getByTestId("attest-submit").click();
    const err = page.getByTestId("attest-error-$statement");
    await expect(err).toHaveText(/Statement is required/);
    await expect(statement).toHaveAttribute("aria-invalid", "true");
    expect(posted).toBeNull();

    await statement.fill("Quarterly access review completed; no exceptions.");
    await expect(err).toHaveCount(0);
    await page.getByTestId("attest-submit").click();

    await expect(page).toHaveURL(new RegExp(`/controls/${CONTROL_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("toast")).toHaveText(/Attestation recorded/, {
      timeout: 30_000,
    });
    expect(posted).toEqual(
      expect.objectContaining({
        statement: "Quarterly access review completed; no exceptions.",
      }),
    );
    // The form is gone from the page, and re-opening it starts empty.
    await expect(page.getByTestId("attest-form")).toHaveCount(0);
    await page.goto(`/controls/${CONTROL_ID}/attest`);
    await expect(page.getByTestId("attest-statement")).toHaveValue("", {
      timeout: 30_000,
    });
  });
});
