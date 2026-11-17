// #2111 — an attestation records pass or fail, and the latest one decides
// the control.
//
// Nothing is mocked: the form posts to the real platform, the attest
// handler evaluates the control after the record lands, and the control
// page reads the new state. `fixtures/e2e/attest-result.sql` seeds a
// manual_attested control used by this spec only. Each attestation is
// stamped "now", so the latest one decides whatever earlier runs left on
// the ledger. Waits are web-first.
//
// Hard rule (P0-A9): neutral test strings only; no vendor-prefixed
// tokens.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const CONTROL_ID = "33333333-3333-3333-3333-33333333a111";

// The attest handler derives its idempotency key from the request body, so
// re-sending an identical statement later (a Playwright retry, a second
// local run) is refused as an idempotency mismatch. A per-run suffix keeps
// each attestation distinct.
const RUN = Date.now().toString(36);

async function attest(
  page: Page,
  result: "pass" | "fail",
  statement: string,
  failReason?: string,
) {
  await page.goto(`/controls/${CONTROL_ID}`);
  await page.getByTestId("control-action-attest").click();
  await expect(page).toHaveURL(new RegExp(`/controls/${CONTROL_ID}/attest$`), {
    timeout: 30_000,
  });
  await page.getByTestId(`attest-result-${result}`).check();
  if (failReason !== undefined) {
    await page.getByTestId("attest-fail-reason").fill(failReason);
  }
  await page.getByTestId("attest-statement").fill(`${statement} (run ${RUN})`);
  await page.getByTestId("attest-submit").click();
  await expect(page).toHaveURL(new RegExp(`/controls/${CONTROL_ID}(\\?|$)`), {
    timeout: 30_000,
  });
}

test.describe("attestation pass / fail (#2111)", () => {
  test.beforeAll(() => {
    seedFromFixture("attest-result");
  });

  test("a fail needs a reason, and nothing is posted without one", async ({
    authedPage: page,
  }) => {
    let posted = false;
    await page.route(`**/api/controls/${CONTROL_ID}/attestations`, (route) => {
      posted = true;
      return route.continue();
    });
    await page.goto(`/controls/${CONTROL_ID}/attest`);
    await expect(page.getByTestId("attest-statement")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId("attest-result-fail").check();
    await page.getByTestId("attest-statement").fill("Review found a gap.");
    await page.getByTestId("attest-submit").click();
    await expect(page.getByTestId("attest-error-$fail_reason")).toHaveText(
      /reason is required/,
    );
    expect(posted).toBe(false);
  });

  test("attest fail and see the control fail; attest pass and see it pass", async ({
    authedPage: page,
  }) => {
    const result = page.getByTestId("control-state-result-value");

    await attest(
      page,
      "fail",
      "Quarterly review found an unapproved administrator.",
      "One administrator account is not on the approved list.",
    );
    await expect(result).toHaveAttribute("data-result", "fail", {
      timeout: 30_000,
    });
    await expect(result).toHaveText("fail");

    await attest(
      page,
      "pass",
      "The unapproved account was removed; access matches the list.",
    );
    await expect(result).toHaveAttribute("data-result", "pass", {
      timeout: 30_000,
    });
    await expect(result).toHaveText("pass");
  });
});
