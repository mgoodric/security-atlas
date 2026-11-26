// #2138 — the risk detail page's layout (front door, #2072).
//
// The page reads as the shared header, a summary strip, then the
// description, mitigation, hierarchy and history sections. These tests
// prove the strip shows the risk's real fields, the description renders
// as Markdown, Mitigation shows each linked control's live state and each
// plan's status, History lists recent changes with names, no id, hash or
// enum key is page text, a viewer gets the page without its write
// affordances, and one failing section leaves the rest standing.
//
// HERMETIC: route-mocks every BFF the page reads (slice 594 convention),
// /api/me and /api/people included, so the viewer and the directory are
// known. The platform side of each read is covered by its own Go tier.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-000000002138";
const PARENT_ID = "00000000-0000-4000-8000-0000000021a1";
const CHILD_ID = "00000000-0000-4000-8000-0000000021a2";
const OWNER = "00000000-0000-0000-0000-0000000021b1";
const APPROVER = "00000000-0000-0000-0000-0000000021b2";
const CONTROL_FAIL = "00000000-0000-4000-8000-0000000021c1";
const CONTROL_PASS = "00000000-0000-4000-8000-0000000021c2";
const PLAN_ID = "00000000-0000-4000-8000-0000000021d1";
const ORG_UNIT = "00000000-0000-4000-8000-0000000021e1";

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
// A hash prefix: seven or more hex characters with a letter and a digit.
const HASH_RE = /\b(?=[0-9a-f]*[a-f])(?=[0-9a-f]*\d)[0-9a-f]{7,}\b/i;
// An enum key: a snake_case token (nist_800_30, in_progress, ...).
const ENUM_KEY_RE = /\b[a-z0-9]+_[a-z0-9_]+\b/;

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

function risk() {
  return {
    id: RISK_ID,
    title: "Layout test risk",
    description:
      "Exports leave the edge **unfiltered**.\n\n- first point\n- second point",
    category: "operational",
    category_name: "Operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 4, impact: 4 },
    severity: 16,
    residual_score: { likelihood: 2, impact: 3 },
    treatment: "accept",
    acceptance_status: "approved",
    acceptance: {
      justification: "Compensated by the edge filter.",
      proposed_until: "2099-03-31",
      proposed_by: OWNER,
      proposed_at: "2026-11-01T10:00:00Z",
      approved_at: "2026-11-02T10:00:00Z",
      self_approved: false,
    },
    accepter: APPROVER,
    accepted_until: "2099-03-31",
    owner_user_id: OWNER,
    owner_unlinked: "",
    treatment_owner: "",
    review_due_at: "2027-01-15T00:00:00Z",
    instrument_reference: "",
    linked_control_ids: [CONTROL_FAIL, CONTROL_PASS],
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-11-02T10:00:00Z",
    level: "org",
    org_unit_id: ORG_UNIT,
    themes: [],
    status: "open",
  };
}

type Opts = {
  approver: boolean;
  failHistory?: boolean;
  failControlState?: boolean;
};

async function mockPage(page: Page, opts: Opts) {
  await page.route("**/api/me", (route: Route) =>
    route.fulfill(
      json({
        user_id: opts.approver ? APPROVER : OWNER,
        tenant_id: "e5e5e5e5-0000-4000-8000-000000002138",
        display_name: opts.approver ? "Abel Approver" : "Vera Viewer",
        email: "viewer@example.test",
        idp_subject: "",
        tenant_role: opts.approver ? "admin" : "viewer",
        time_zone: null,
        theme: null,
        is_admin: false,
        is_tenant_admin: opts.approver,
        is_approver: opts.approver,
        has_tenant_role: true,
        owner_roles: [],
        roles: [opts.approver ? "admin" : "viewer"],
      }),
    ),
  );
  await page.route(/\/api\/people(\?.*)?$/, (route: Route) =>
    route.fulfill(
      json({
        users: [
          { id: OWNER, display_name: "Olive Owner", status: "active" },
          { id: APPROVER, display_name: "Abel Approver", status: "active" },
        ],
        api_keys: [],
      }),
    ),
  );
  await page.route("**/api/risks/categories**", (route: Route) =>
    route.fulfill(
      json({
        categories: [
          {
            id: "c1",
            slug: "operational",
            name: "Operational",
            archived: false,
            is_default: true,
          },
        ],
      }),
    ),
  );
  await page.route(/\/api\/controls-list(\?.*)?$/, (route: Route) =>
    route.fulfill(
      json({
        controls: [
          {
            id: CONTROL_FAIL,
            title: "Edge egress filtering",
            control_family: "Network",
            scf_id: "NET-01",
            lifecycle_state: "active",
            bundle_id: "b1",
          },
          {
            id: CONTROL_PASS,
            title: "Quarterly access review",
            control_family: "Identity",
            scf_id: "IAC-01",
            lifecycle_state: "active",
            bundle_id: "b1",
          },
        ],
        count: 2,
      }),
    ),
  );
  await page.route(`**/api/controls/${CONTROL_FAIL}/state`, (route: Route) =>
    opts.failControlState
      ? route.fulfill(json({ error: "upstream down" }, 500))
      : route.fulfill(
          json({
            control_id: CONTROL_FAIL,
            states: [
              {
                scope_cell_id: null,
                result: "pass",
                freshness_status: "fresh",
                evidence_count_in_window: 3,
                last_observed_at: "2026-11-10T00:00:00Z",
                evaluated_at: "2026-11-10T00:00:00Z",
                freshness_class: "daily",
                trigger: "scheduled",
              },
              {
                scope_cell_id: "00000000-0000-4000-8000-0000000021f1",
                result: "fail",
                freshness_status: "stale",
                evidence_count_in_window: 1,
                last_observed_at: "2026-11-01T00:00:00Z",
                evaluated_at: "2026-11-10T00:00:00Z",
                freshness_class: "daily",
                trigger: "scheduled",
              },
            ],
            count: 2,
          }),
        ),
  );
  await page.route(`**/api/controls/${CONTROL_PASS}/state`, (route: Route) =>
    route.fulfill(
      json({
        control_id: CONTROL_PASS,
        states: [
          {
            scope_cell_id: null,
            result: "pass",
            freshness_status: "fresh",
            evidence_count_in_window: 2,
            last_observed_at: "2026-11-10T00:00:00Z",
            evaluated_at: "2026-11-10T00:00:00Z",
            freshness_class: "daily",
            trigger: "scheduled",
          },
        ],
        count: 1,
      }),
    ),
  );
  await page.route(`**/api/risks/${RISK_ID}/action-plans`, (route: Route) =>
    route.fulfill(
      json({
        action_plans: [
          {
            id: PLAN_ID,
            title: "Deploy the egress proxy",
            status: "in_progress",
            due_date: "2027-02-01",
          },
        ],
        count: 1,
      }),
    ),
  );
  await page.route(`**/api/risks/${RISK_ID}/parents`, (route: Route) =>
    route.fulfill(
      json({
        parents: [
          {
            id: PARENT_ID,
            title: "Company data loss",
            level: "company",
            severity: 20,
            link_source: "manual",
          },
        ],
        count: 1,
      }),
    ),
  );
  await page.route(`**/api/risks/${RISK_ID}/children`, (route: Route) =>
    route.fulfill(
      json({
        children: [
          {
            id: CHILD_ID,
            title: "Team export gap",
            level: "team",
            severity: 9,
            link_source: "manual",
          },
        ],
        count: 1,
      }),
    ),
  );
  await page.route(/\/api\/risks-hierarchy\/org-units(\?.*)?$/, (route) =>
    route.fulfill(
      json({
        org_units: [
          {
            id: ORG_UNIT,
            name: "Platform",
            level: "org",
            acceptance_authorities: [],
          },
        ],
      }),
    ),
  );
  await page.route(/\/api\/risks(\?.*)?$/, (route: Route) =>
    route.fulfill(json({ risks: [], count: 0 })),
  );
  await page.route(`**/api/risks/${RISK_ID}/audit-log**`, (route: Route) =>
    opts.failHistory
      ? route.fulfill(json({ error: "audit log unavailable" }, 500))
      : route.fulfill(
          json({
            entries: [
              {
                id: "00000000-0000-4000-8000-0000000021f2",
                actor: APPROVER,
                action: "acceptance_approved",
                before: { acceptance_status: "proposed" },
                after: {
                  acceptance_status: "approved",
                  accepter: APPROVER,
                },
                created_at: "2026-11-02T10:00:00Z",
              },
              {
                id: "00000000-0000-4000-8000-0000000021f3",
                actor: OWNER,
                action: "control_linked",
                before: { control_id: null },
                after: { control_id: CONTROL_FAIL },
                created_at: "2026-11-01T09:00:00Z",
              },
              {
                id: "00000000-0000-4000-8000-0000000021f4",
                actor: "system:risk-deriver",
                action: "placement_changed",
                before: { level: "team", org_unit_id: null },
                after: { level: "org", org_unit_id: ORG_UNIT },
                created_at: "2026-10-15T09:00:00Z",
              },
            ],
            count: 3,
            limit: 200,
            truncated: false,
          }),
        ),
  );
  await page.route(`**/api/risks/${RISK_ID}`, (route: Route) =>
    route.fulfill(json({ risk: risk() })),
  );
}

async function openPage(page: Page) {
  await page.goto(`/risks/${RISK_ID}`);
  await expect(page.getByTestId("risk-detail-title")).toHaveText(
    "Layout test risk",
    { timeout: 30_000 },
  );
}

async function expectNoRawValues(page: Page) {
  const text = await page.getByTestId("risk-detail").innerText();
  expect(text, "page shows a UUID").not.toMatch(UUID_RE);
  expect(text, "page shows a hash prefix").not.toMatch(HASH_RE);
  expect(text, "page shows an enum key").not.toMatch(ENUM_KEY_RE);
}

test.describe("/risks/[id] layout (#2138)", () => {
  test("strip, Markdown description, mitigation states, hierarchy and named history", async ({
    authedPage: page,
  }) => {
    await mockPage(page, { approver: true });
    await openPage(page);

    // The sections, in order.
    await expect(page.getByTestId("risk-detail-section")).toHaveCount(4);
    const order = await page
      .getByTestId("risk-detail-section")
      .evaluateAll((els) => els.map((e) => e.getAttribute("data-section")));
    expect(order).toEqual([
      "description",
      "mitigation",
      "hierarchy",
      "history",
    ]);

    // The summary strip shows the risk's real fields.
    const strip = page.getByTestId("risk-detail-summary");
    await expect(strip.getByTestId("risk-detail-severity")).toHaveText("16");
    await expect(strip.getByTestId("risk-detail-residual")).toHaveText("6");
    await expect(strip.getByTestId("risk-detail-treatment")).toHaveText(
      "Accept (approved)",
    );
    await expect(strip.getByTestId("risk-detail-acceptance-state")).toHaveText(
      "Approved",
    );
    await expect(strip.getByTestId("risk-detail-owner")).toHaveText(
      "Olive Owner",
    );
    await expect(strip.getByTestId("risk-detail-review-due")).toHaveText(
      "Jan 15, 2027",
    );
    await expect(strip.getByTestId("risk-detail-status")).toHaveText("Open");

    // The description renders as Markdown.
    const description = page.getByTestId("risk-detail-description");
    await expect(description.locator("strong")).toHaveText("unfiltered");
    await expect(description.locator("li")).toHaveText([
      "first point",
      "second point",
    ]);

    // Mitigation: each control's live state (worst cell wins) and the
    // plan's status, by title.
    const failRow = page.getByTestId(`risk-controls-row-${CONTROL_FAIL}`);
    await expect(failRow.getByRole("link")).toHaveText("Edge egress filtering");
    await expect(failRow.getByTestId("risk-controls-state")).toHaveText("Fail");
    await expect(failRow.getByTestId("risk-controls-freshness")).toHaveText(
      "Stale",
    );
    const passRow = page.getByTestId(`risk-controls-row-${CONTROL_PASS}`);
    await expect(passRow.getByTestId("risk-controls-state")).toHaveText("Pass");
    const plan = page.getByTestId("linked-action-plan");
    await expect(plan.getByRole("link")).toHaveText("Deploy the egress proxy");
    await expect(plan.getByTestId("linked-action-plan-status")).toHaveText(
      "In progress",
    );

    // Hierarchy: parent and child by title and level label.
    await expect(page.getByTestId("risk-detail-parent")).toContainText(
      "Company data loss",
    );
    await expect(page.getByTestId("risk-detail-parent")).toContainText(
      "Company",
    );
    await expect(page.getByTestId("risk-detail-child")).toContainText(
      "Team export gap",
    );

    // History: recent changes, by name, with titles in place of ids.
    const entries = page.getByTestId("risk-history-entry");
    await expect(entries).toHaveCount(3);
    await expect(entries.nth(0)).toContainText("Acceptance approved");
    await expect(entries.nth(0).getByTestId("risk-history-actor")).toHaveText(
      "by Abel Approver",
    );
    await expect(entries.nth(1).getByTestId("risk-history-change")).toHaveText(
      "Control: none → Edge egress filtering",
    );
    await expect(entries.nth(2).getByTestId("risk-history-actor")).toHaveText(
      "by System",
    );
    await expect(entries.nth(2).getByTestId("risk-history-change")).toHaveText([
      "Level: Team → Organisation",
      "Org unit: none → Platform",
    ]);

    // The acceptance names its proposer and approver.
    await expect(
      page.getByTestId("risk-detail-acceptance-proposed"),
    ).toContainText("Olive Owner");
    await expect(
      page.getByTestId("risk-detail-acceptance-approved"),
    ).toContainText("Abel Approver");

    // An approver gets the write affordances.
    await expect(page.getByTestId("risk-detail-edit")).toBeVisible();
    await expect(page.getByTestId("risk-controls-link-open")).toBeVisible();
    await expect(page.getByTestId("linked-action-plans-add")).toBeVisible();
    await expect(page.getByTestId("risk-detail-attach")).toBeVisible();

    await expectNoRawValues(page);
  });

  test("a viewer reads the page without its write affordances", async ({
    authedPage: page,
  }) => {
    await mockPage(page, { approver: false });
    await openPage(page);

    await expect(
      page
        .getByTestId(`risk-controls-row-${CONTROL_FAIL}`)
        .getByTestId("risk-controls-state"),
    ).toHaveText("Fail");
    await expect(page.getByTestId("risk-history-entry")).toHaveCount(3);

    for (const id of [
      "risk-detail-edit",
      "risk-detail-revoke-acceptance",
      "risk-detail-owner-pick-open",
      "risk-controls-link-open",
      `risk-controls-unlink-${CONTROL_FAIL}`,
      "linked-action-plans-add",
      "risk-detail-attach",
      `risk-detail-child-detach-${CHILD_ID}`,
    ]) {
      await expect(page.getByTestId(id), `${id} hidden`).toHaveCount(0);
    }
    await expectNoRawValues(page);
  });

  test("a failing section shows its own error and the rest renders", async ({
    authedPage: page,
  }) => {
    await mockPage(page, {
      approver: true,
      failHistory: true,
      failControlState: true,
    });
    await openPage(page);

    await expect(page.getByTestId("risk-history-error")).toBeVisible();
    await expect(
      page
        .getByTestId(`risk-controls-row-${CONTROL_FAIL}`)
        .getByTestId("risk-controls-state-error"),
    ).toHaveText("State unavailable");

    // The other sections still stand.
    await expect(
      page.getByTestId("risk-detail-description").locator("strong"),
    ).toHaveText("unfiltered");
    await expect(
      page
        .getByTestId(`risk-controls-row-${CONTROL_PASS}`)
        .getByTestId("risk-controls-state"),
    ).toHaveText("Pass");
    await expect(page.getByTestId("linked-action-plan")).toHaveCount(1);
    await expect(page.getByTestId("risk-detail-child")).toHaveCount(1);
    await expect(
      page.getByTestId("risk-detail-summary").getByTestId("risk-detail-status"),
    ).toHaveText("Open");
  });
});
