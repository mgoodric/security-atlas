// #2465 — Playwright E2E for acknowledging a policy from the web app.
//
//   1. A published policy on the caller's pending list shows the "Your
//      acknowledgment" card; clicking Acknowledge POSTs the BFF with only
//      the version it shows (an ack is always the caller's own; #2668
//      pins the version) and the card then says the version was
//      acknowledged.
//   2. A published policy not on the pending list (outside the caller's
//      audience, or already acknowledged) shows no card and no button.
//   3. The policy list names the policies waiting for the caller, each
//      linking to its detail page.
//
// Hermetic mock discipline (slice 594): the detail, list, pending and
// acknowledge BFFs are route-mocked with bodies hand-written to the
// production wire shapes (`wireFromPolicy` in
// internal/api/policies/handlers.go; `pendingResponse` and `ackResponse`
// in internal/api/policyacks/handlers.go). Which roles the platform admits
// is pinned by the Go role matrix (role_matrix_2524_integration_test.go).

import { expect, test } from "./fixtures";

const PENDING_ID = "00000000-0000-0000-0000-000000002465";
const OTHER_ID = "00000000-0000-0000-0000-000000002466";

function policy(id: string, title: string) {
  return {
    id,
    title,
    version: "2.0",
    body_md: `# ${title}\n`,
    owner_role: "admin",
    approver_role: "admin",
    linked_control_ids: ["00000000-0000-0000-0000-00000000c001"],
    acknowledgment_required_roles: ["everyone"],
    status: "published",
    source_attribution: "tenant_authored",
    created_by: "user-1",
    effective_date: "2026-11-01",
    published_at: "2026-11-01T00:00:00Z",
    imported: false,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
  };
}

const ACK_RATE = {
  numerator: 3,
  denominator: 10,
  percent: 30,
  window_seconds: 31536000,
};

const PENDING_BODY = {
  pending: [
    {
      policy_id: PENDING_ID,
      // The platform keys a version by its policy row (`policy_version_id = p.id`).
      policy_version_id: PENDING_ID,
      title: "Acceptable Use Policy",
      version: "2.0",
      effective_date: "2026-11-01",
      required_roles: ["everyone"],
    },
  ],
  count: 1,
  window_seconds: 31536000,
};

test.describe("policy acknowledgment (#2465)", () => {
  test.beforeEach(async ({ authedPage: page }) => {
    // `**` after the path also matches the detail page's ?include=current.
    await page.route("**/api/me/acknowledgments**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(PENDING_BODY),
      }),
    );
    for (const [id, title] of [
      [PENDING_ID, "Acceptable Use Policy"],
      [OTHER_ID, "Engineering Standards"],
    ]) {
      await page.route(`**/api/policies/${id}`, (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            policy: policy(id, title),
            ack_rate: ACK_RATE,
          }),
        }),
      );
    }
    await page.route("**/api/policies", (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          policies: [
            {
              ...policy(PENDING_ID, "Acceptable Use Policy"),
              ack_rate: ACK_RATE,
            },
            {
              ...policy(OTHER_ID, "Engineering Standards"),
              ack_rate: ACK_RATE,
            },
          ],
        }),
      });
    });
  });

  test("a pending policy is acknowledged with one click, for the caller", async ({
    authedPage: page,
  }) => {
    const posts: (string | null)[] = [];
    await page.route(`**/api/policies/${PENDING_ID}/acknowledge`, (route) => {
      posts.push(route.request().postData());
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          id: "00000000-0000-0000-0000-00000000b465",
          policy_id: PENDING_ID,
          policy_version_id: PENDING_BODY.pending[0].policy_version_id,
          user_id: "00000000-0000-0000-0000-00000000c465",
          acknowledged_at: "2026-11-18T10:00:00Z",
          ack_token: "tok-2465",
          deduplicated: false,
          evidence_emission_ok: true,
        }),
      });
    });

    await page.goto(`/policies/${PENDING_ID}`);
    const card = page.getByTestId("policy-detail-my-ack");
    await expect(card).toContainText("Your acknowledgment is needed");
    await expect(card).toContainText("version 2.0");

    await page.getByTestId("policy-action-acknowledge").click();
    await expect(page.getByTestId("policy-detail-my-ack-done")).toContainText(
      "You acknowledged version 2.0 on",
    );
    await expect(page.getByTestId("policy-action-acknowledge")).toHaveCount(0);
    expect(posts).toHaveLength(1);
    expect(JSON.parse(posts[0] ?? "null")).toEqual({
      policy_version_id: PENDING_BODY.pending[0].policy_version_id,
    });
  });

  test("a policy not waiting for the caller offers no Acknowledge button", async ({
    authedPage: page,
  }) => {
    await page.goto(`/policies/${OTHER_ID}`);
    await expect(page.getByTestId("policy-detail-title")).toContainText(
      "Engineering Standards",
    );
    await expect(page.getByTestId("policy-detail-my-ack")).toHaveCount(0);
    await expect(page.getByTestId("policy-action-acknowledge")).toHaveCount(0);
  });

  // #2668 — after a reload the platform's ?include=current list carries
  // the ack, so the card still says which version was acknowledged.
  test("an acknowledged policy still says so after a reload", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/me/acknowledgments**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          pending: [],
          count: 0,
          window_seconds: 31536000,
          current: [
            {
              policy_id: OTHER_ID,
              policy_version_id: OTHER_ID,
              title: "Engineering Standards",
              version: "2.0",
              acknowledged_at: "2026-11-18T10:00:00Z",
            },
          ],
        }),
      }),
    );
    await page.goto(`/policies/${OTHER_ID}`);
    await expect(page.getByTestId("policy-detail-my-ack-done")).toContainText(
      "You acknowledged version 2.0 on",
    );
    await expect(page.getByTestId("policy-action-acknowledge")).toHaveCount(0);
  });

  test("an ack whose evidence was not recorded says so", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/policies/${PENDING_ID}/acknowledge`, (route) =>
      route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          id: "00000000-0000-0000-0000-00000000b466",
          policy_id: PENDING_ID,
          policy_version_id: PENDING_ID,
          user_id: "00000000-0000-0000-0000-00000000c465",
          acknowledged_at: "2026-11-18T10:00:00Z",
          ack_token: "tok-2668",
          deduplicated: false,
          evidence_emission_ok: false,
          evidence_emission_error: "ingest unavailable",
        }),
      }),
    );
    await page.goto(`/policies/${PENDING_ID}`);
    await page.getByTestId("policy-action-acknowledge").click();
    await expect(page.getByTestId("toast")).toContainText(
      "Policy acknowledged, but its evidence record was not saved",
    );
    await expect(page.getByTestId("policy-detail-my-ack-done")).toBeVisible();
  });

  test("a version re-published since the page loaded is refused", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/policies/${PENDING_ID}/acknowledge`, (route) =>
      route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error:
            "policy_version_id is not the version this policy id names; reload the policy and acknowledge the version shown",
        }),
      }),
    );
    await page.goto(`/policies/${PENDING_ID}`);
    await page.getByTestId("policy-action-acknowledge").click();
    await expect(page.getByTestId("toast")).toContainText(
      "Could not acknowledge policy",
    );
    await expect(page.getByTestId("policy-detail-my-ack-done")).toHaveCount(0);
  });

  test("the policy list names the policies waiting for the caller", async ({
    authedPage: page,
  }) => {
    await page.goto("/policies");
    await expect(page.getByTestId("policies-my-acks-count")).toHaveText(
      "1 policy waits for your acknowledgment:",
    );
    const link = page.getByTestId("policies-my-acks-link");
    await expect(link).toHaveText("Acceptable Use Policy");
    await expect(link).toHaveAttribute("href", `/policies/${PENDING_ID}`);
  });
});
