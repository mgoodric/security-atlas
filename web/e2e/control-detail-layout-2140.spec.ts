// #2140 — the control detail page's layout (front door, #2072).
//
// The page reads as the shared header, a state strip, then Evidence,
// Coverage and Linked sections; the framework graph is on its own
// "Framework map" tab. These tests prove the strip shows the owner and an
// exception marker for an excepted control, an evidence row opens its
// detail, Linked lists the exception, the graph renders only on the map
// tab, no "unavailable" notice shows when the AI explanation is
// unavailable, no id, hash or enum key is page text, a viewer reads the
// page without its write affordances or the exceptions it may not read,
// and one failing section leaves the rest standing.
//
// HERMETIC: route-mocks every BFF the page reads (slice 594 convention),
// /api/me and /api/people included, so the viewer and the directory are
// known. The module-off case needs the server-side /v1/me/modules read
// and lives in module-links.spec.ts.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const CONTROL_ID = "00000000-0000-4000-8000-000000002140";
const ANCHOR_ID = "00000000-0000-4000-8000-0000000021a0";
const OWNER = "00000000-0000-0000-0000-0000000021b1";
const APPROVER = "00000000-0000-0000-0000-0000000021b2";
const FV_SOC2 = "00000000-0000-4000-8000-0000000021c1";
const FV_ISO = "00000000-0000-4000-8000-0000000021c2";
const REQ_CC61 = "00000000-0000-4000-8000-0000000021d1";
const REQ_A8 = "00000000-0000-4000-8000-0000000021d2";
const EVIDENCE_ID = "00000000-0000-4000-8000-0000000021e1";
const EXCEPTION_ID = "00000000-0000-4000-8000-0000000021f1";
const OTHER_EXCEPTION_ID = "00000000-0000-4000-8000-0000000021f6";
const OTHER_CONTROL_ID = "00000000-0000-4000-8000-0000000021f7";
const RISK_ID = "00000000-0000-4000-8000-0000000021f2";
const PLAN_ID = "00000000-0000-4000-8000-0000000021f3";
const POLICY_ID = "00000000-0000-4000-8000-0000000021f4";
const SCOPE_ID = "00000000-0000-4000-8000-0000000021f5";

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
// A hash prefix: seven or more hex characters with a letter and a digit.
const HASH_RE = /\b(?=[0-9a-f]*[a-f])(?=[0-9a-f]*\d)[0-9a-f]{7,}\b/i;
// An enum key: a snake_case token (in_progress, no_evidence, ...).
const ENUM_KEY_RE = /\b[a-z0-9]+_[a-z0-9_]+\b/;

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

function requirement(over: Record<string, unknown>) {
  return {
    edge_id: "",
    requirement_id: "",
    code: "",
    title: "",
    framework_slug: "soc2",
    framework_name: "SOC 2",
    framework_version: "2017",
    framework_version_id: FV_SOC2,
    framework_version_status: "current",
    relationship_type: "equal",
    strength: 1,
    coverage: 0.9,
    source_attribution: "community_draft",
    origin: "catalog",
    ...over,
  };
}

function coverage() {
  return {
    control: {
      id: CONTROL_ID,
      bundle_id: "access-review",
      version: "1.0.0",
      scf_anchor_id: ANCHOR_ID,
      title: "Quarterly access review",
      control_family: "Identity",
      implementation_type: "manual_periodic",
      lifecycle_state: "active",
      owner_role: "Security Engineering",
      freshness_class: "quarterly",
      owner_user_id: OWNER,
    },
    anchor: {
      id: ANCHOR_ID,
      scf_id: "IAC-17",
      family: "IAC",
      name: "Periodic review of account privileges",
    },
    requirements: [
      requirement({
        edge_id: "e1",
        requirement_id: REQ_CC61,
        code: "CC6.1",
        title: "Logical access security",
      }),
      requirement({
        edge_id: "e2",
        requirement_id: REQ_A8,
        code: "A.8.2",
        title: "Privileged access rights",
        framework_slug: "iso27001",
        framework_name: "ISO/IEC 27001",
        framework_version: "2022",
        framework_version_id: FV_ISO,
        coverage: null,
        exclusion: {
          framework_scope_id: SCOPE_ID,
          justification: "No privileged accounts in scope",
          decided_at: "2026-10-01T00:00:00Z",
        },
      }),
    ],
  };
}

function stateCell(result: string, freshness: string) {
  return {
    scope_cell_id: null,
    result,
    freshness_status: freshness,
    evidence_count_in_window: 1,
    last_observed_at: "2026-11-10T00:00:00Z",
    evaluated_at: "2026-11-10T00:00:00Z",
    freshness_class: "quarterly",
    trigger: "scheduled",
  };
}

type Opts = {
  approver: boolean;
  // The platform refuses the caller the exceptions read (a viewer).
  exceptionsRefused?: boolean;
  failEvidence?: boolean;
  failRisks?: boolean;
  // The control passes and has no exception of its own; only another
  // control's active exception comes back from the list.
  passing?: boolean;
};

async function mockPage(page: Page, opts: Opts) {
  await page.route("**/api/me", (route: Route) =>
    route.fulfill(
      json({
        user_id: opts.approver ? APPROVER : OWNER,
        tenant_id: "e5e5e5e5-0000-4000-8000-000000002140",
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

  const base = `**/api/controls/${CONTROL_ID}`;
  await page.route(`${base}/coverage`, (route: Route) =>
    route.fulfill(json(coverage())),
  );
  await page.route(`${base}/state`, (route: Route) =>
    route.fulfill(
      json({
        control_id: CONTROL_ID,
        states: opts.passing
          ? [stateCell("pass", "fresh")]
          : [stateCell("excepted", "fresh"), stateCell("excepted", "stale")],
        count: 2,
      }),
    ),
  );
  await page.route(`${base}/effective-scope**`, (route: Route) => {
    const fv =
      new URL(route.request().url()).searchParams.get("framework_version") ??
      FV_SOC2;
    return route.fulfill(
      json({
        control_id: CONTROL_ID,
        framework_version_id: fv,
        framework_scope_id: null,
        effective_scope: [],
        effective_scope_count: 3,
        in_scope: true,
      }),
    );
  });
  await page.route(`${base}/policies`, (route: Route) =>
    route.fulfill(
      json({
        control_id: CONTROL_ID,
        policies: [
          {
            policy_id: POLICY_ID,
            title: "Access control policy",
            version: "1.2.0",
            status: "published",
          },
        ],
        count: 1,
      }),
    ),
  );
  await page.route(`${base}/risks`, (route: Route) =>
    opts.failRisks
      ? route.fulfill(json({ error: "risks unavailable" }, 500))
      : route.fulfill(
          json({
            control_id: CONTROL_ID,
            risks: [
              {
                risk_id: RISK_ID,
                title: "Stale administrator access",
                methodology: "nist_800_30",
                inherent_score: { likelihood: 4, impact: 4 },
                residual_score: { likelihood: 2, impact: 3 },
                link_weight: null,
              },
            ],
            count: 1,
          }),
        ),
  );
  await page.route(`${base}/action-plans`, (route: Route) =>
    route.fulfill(
      json({
        action_plans: [
          {
            id: PLAN_ID,
            title: "Automate the access review",
            status: "in_progress",
            due_date: "2027-02-01",
          },
        ],
        count: 1,
      }),
    ),
  );
  await page.route(`${base}/history**`, (route: Route) =>
    route.fulfill(json({ control_id: CONTROL_ID, history: [], count: 0 })),
  );
  // The AI explanation and summary are unavailable: one read fails, the
  // other comes back suppressed.
  await page.route(`${base}/gap-explanation`, (route: Route) =>
    route.fulfill(json({ error: "generation unavailable" }, 503)),
  );
  await page.route(`${base}/evidence-summary`, (route: Route) =>
    route.fulfill(
      json({
        control_id: CONTROL_ID,
        evidence: { records: [], bound: 0, total: 0 },
        summary: null,
        suppressed_reason: "generation_unavailable",
      }),
    ),
  );
  await page.route(/\/api\/evidence\?.*control_id=/, (route: Route) =>
    opts.failEvidence
      ? route.fulfill(json({ error: "ledger unavailable" }, 500))
      : route.fulfill(
          json({
            control_id: CONTROL_ID,
            evidence: [
              {
                evidence_id: EVIDENCE_ID,
                evidence_kind: "manual.attestation",
                observed_at: "2026-11-09T12:00:00Z",
                source: { actor_type: "user", actor_id: OWNER },
                content_hash: "9f2c4e1a7b3d5f60",
                scope_cell: null,
                result: "pass",
              },
            ],
            count: 1,
            total: 1,
            next_cursor: "",
          }),
        ),
  );
  await page.route(`**/api/evidence/${EVIDENCE_ID}`, (route: Route) =>
    route.fulfill(
      json({
        evidence: {
          evidence_id: EVIDENCE_ID,
          evidence_kind: "manual.attestation",
          schema_version: "1",
          observed_at: "2026-11-09T12:00:00Z",
          ingested_at: "2026-11-09T12:00:01Z",
          result: "pass",
          payload: {
            statement: "Access matches the approved list.",
            result: "pass",
          },
          payload_uri: null,
          source: { actor_type: "user", actor_id: OWNER },
          source_attribution: null,
          credential_id: null,
          ingestion_path: "attestation",
          control_id: CONTROL_ID,
          control_ref: "Quarterly access review",
          scope_cell: null,
          content_hash: "9f2c4e1a7b3d5f60",
        },
      }),
    ),
  );
  await page.route(/\/api\/exceptions\?.*control_id=/, (route: Route) =>
    opts.exceptionsRefused
      ? route.fulfill(json({ error: "forbidden" }, 403))
      : route.fulfill(
          json({
            exceptions: [
              {
                id: EXCEPTION_ID,
                control_id: CONTROL_ID,
                scope_cell_predicate: { op: "true" },
                justification: "Legacy directory retires in Q1",
                compensating_controls: [],
                requested_by: OWNER,
                requested_at: "2026-10-01T00:00:00Z",
                approved_by: APPROVER,
                approved_at: "2026-10-02T00:00:00Z",
                expires_at: "2027-03-31T00:00:00Z",
                status: "active",
                created_at: "2026-10-01T00:00:00Z",
                updated_at: "2026-10-02T00:00:00Z",
              },
              // Another control's exception: the page must not list it even
              // when a list comes back unfiltered.
              {
                id: OTHER_EXCEPTION_ID,
                control_id: OTHER_CONTROL_ID,
                scope_cell_predicate: { op: "true" },
                justification: "Other control's waiver",
                compensating_controls: [],
                requested_by: OWNER,
                requested_at: "2026-10-01T00:00:00Z",
                expires_at: "2027-03-31T00:00:00Z",
                status: "active",
                created_at: "2026-10-01T00:00:00Z",
                updated_at: "2026-10-02T00:00:00Z",
              },
            ].filter((e) => !(opts.passing && e.control_id === CONTROL_ID)),
            count: opts.passing ? 1 : 2,
          }),
        ),
  );
}

async function openPage(page: Page, query = "") {
  await page.goto(`/controls/${CONTROL_ID}${query}`);
  await expect(page.getByTestId("control-title")).toHaveText(
    "Quarterly access review",
    { timeout: 30_000 },
  );
}

async function expectNoRawValues(page: Page) {
  const text = await page.getByTestId("control-detail").innerText();
  expect(text, "page shows a UUID").not.toMatch(UUID_RE);
  expect(text, "page shows a hash prefix").not.toMatch(HASH_RE);
  expect(text, "page shows an enum key").not.toMatch(ENUM_KEY_RE);
}

test.describe("/controls/[id] layout (#2140)", () => {
  test("strip, evidence, coverage and linked; an evidence row opens its detail", async ({
    authedPage: page,
  }) => {
    await mockPage(page, { approver: true });
    await openPage(page);

    // The sections, in order.
    const sections = page.getByTestId("control-detail-section");
    await expect(sections).toHaveCount(3);
    expect(
      await sections.evaluateAll((els) =>
        els.map((e) => e.getAttribute("data-section")),
      ),
    ).toEqual(["evidence", "coverage", "linked"]);

    // The state strip: state, freshness, owner, and the exception and
    // action-plan markers.
    const strip = page.getByTestId("control-state-strip");
    await expect(strip.getByTestId("control-state-result-value")).toHaveText(
      "Excepted",
    );
    await expect(strip.getByTestId("control-strip-freshness")).toHaveText(
      "Stale",
    );
    await expect(strip.getByTestId("control-owner")).toHaveText("Olive Owner");
    await expect(strip.getByTestId("control-owner-role")).toHaveText(
      "Security Engineering",
    );
    await expect(strip.getByTestId("control-strip-exception")).toBeVisible();
    await expect(strip.getByTestId("control-strip-action-plan")).toHaveText(
      "Open action plan",
    );

    // Coverage: the requirements reached, the SoA exclusion and the
    // covered-by-exception marker.
    const coverageSection = page.getByTestId("coverage-section");
    await expect(coverageSection.getByTestId("coverage-row")).toHaveCount(2);
    await expect(
      coverageSection.getByTestId("coverage-excepted"),
    ).toBeVisible();
    const excluded = coverageSection.locator(
      '[data-testid="coverage-row"][data-excluded="true"]',
    );
    await expect(excluded).toHaveCount(1);
    await expect(excluded.getByTestId("coverage-soa-exclusion")).toContainText(
      "No privileged accounts in scope",
    );

    // Linked: risks, the exception, the plan and the policy, by title.
    await expect(page.getByTestId("risk-row")).toContainText(
      "Stale administrator access",
    );
    // Only this control's exception: the page asks for its own, and drops
    // the other control's row the mock returns beside it.
    const exception = page.getByTestId("exception-row");
    await expect(exception).toHaveCount(1);
    await expect(page.getByText("Other control's waiver")).toHaveCount(0);
    await expect(exception.getByTestId("exception-row-link")).toHaveText(
      "Legacy directory retires in Q1",
    );
    await expect(exception.getByTestId("exception-row-status")).toHaveText(
      "Active",
    );
    await expect(
      page.getByTestId("linked-action-plan").getByRole("link"),
    ).toHaveText("Automate the access review");
    await expect(page.getByTestId("policy-row")).toContainText(
      "Access control policy",
    );

    // An approver gets the write affordances.
    for (const id of [
      "control-action-attest",
      "control-action-evaluate",
      "control-action-upload-toggle",
      "control-action-request-exception",
    ]) {
      await expect(page.getByTestId(id), `${id} shown`).toBeVisible();
    }

    await expectNoRawValues(page);

    // An evidence row opens its detail.
    const row = page.getByTestId("evidence-stream-row");
    await expect(row.getByTestId("evidence-stream-row-source")).toHaveText(
      "Olive Owner",
    );
    await expect(row.getByTestId("evidence-stream-row-result")).toHaveText(
      "Pass",
    );
    await row.click();
    await expect(page).toHaveURL(new RegExp(`/evidence/${EVIDENCE_ID}$`));
    await expect(page.getByTestId("evidence-detail-result")).toHaveText(
      "Pass",
      { timeout: 30_000 },
    );
  });

  test("the graph renders on the Framework map tab and nowhere on the main tab", async ({
    authedPage: page,
  }) => {
    await mockPage(page, { approver: true });
    await openPage(page);

    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible();
    await expect(page.getByTestId("coverage-section")).toBeVisible();
    await expect(page.getByTestId("ucf-mini-viz")).toHaveCount(0);
    await expect(page.getByTestId("ucf-viz-section")).toHaveCount(0);

    await page.getByTestId("control-tab-map").click();
    await expect(page).toHaveURL(/\?tab=map$/);
    await expect(page.getByTestId("control-tab-panel-map")).toBeVisible();
    await expect(page.getByTestId("ucf-mini-viz")).toBeVisible();
    await expect(page.getByTestId("ucf-viz-anchor")).toContainText("IAC-17");
    await expect(page.getByTestId("ucf-viz-requirement")).toHaveCount(2);
    // The map tab is the graph alone, not the main sections.
    await expect(page.getByTestId("control-detail-section")).toHaveCount(0);

    // The tab is URL-addressable.
    await openPage(page, "?tab=map");
    await expect(page.getByTestId("ucf-mini-viz")).toBeVisible();
    await expect(page.getByTestId("control-tab-map")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  test("with the AI explanation unavailable, no unavailable notice appears", async ({
    authedPage: page,
  }) => {
    await mockPage(page, { approver: true });
    const explained = page.waitForResponse((r) =>
      r.url().endsWith(`/api/controls/${CONTROL_ID}/gap-explanation`),
    );
    const summarised = page.waitForResponse((r) =>
      r.url().endsWith(`/api/controls/${CONTROL_ID}/evidence-summary`),
    );
    await openPage(page);
    await explained;
    await summarised;
    await expect(page.getByTestId("evidence-stream-row")).toHaveCount(1);

    await expect(page.getByTestId("gap-explanation-section")).toHaveCount(0);
    await expect(page.getByTestId("evidence-summary-section")).toHaveCount(0);
    await expect(page.getByText(/unavailable/i)).toHaveCount(0);
  });

  test("a viewer reads the page without write affordances or the exceptions it may not read", async ({
    authedPage: page,
  }) => {
    await mockPage(page, { approver: false, exceptionsRefused: true });
    await openPage(page);

    await expect(page.getByTestId("coverage-row")).toHaveCount(2);
    await expect(page.getByTestId("risk-row")).toHaveCount(1);
    await expect(page.getByTestId("control-owner")).toHaveText("Olive Owner");
    // The marker rests on the evaluated state the viewer may read.
    await expect(page.getByTestId("control-strip-exception")).toBeVisible();

    // No Exceptions part and no error for the refused read.
    await expect(page.getByTestId("exceptions-section")).toHaveCount(0);
    await expect(page.getByTestId("exceptions-error")).toHaveCount(0);
    await expect(page.getByText("Legacy directory retires in Q1")).toHaveCount(
      0,
    );

    for (const id of [
      "control-action-attest",
      "control-action-evaluate",
      "control-action-upload-toggle",
      "control-action-request-exception",
    ]) {
      await expect(page.getByTestId(id), `${id} hidden`).toHaveCount(0);
    }
    await expect(
      page.getByTestId("control-action-download-bundle"),
    ).toBeVisible();
    await expectNoRawValues(page);
  });

  test("another control's active exception raises no marker and is not listed", async ({
    authedPage: page,
  }) => {
    await mockPage(page, { approver: true, passing: true });
    const asked = page.waitForRequest((r) =>
      /\/api\/exceptions\?/.test(r.url()),
    );
    await openPage(page);
    // The page asks for this control's exceptions only.
    expect(new URL((await asked).url()).searchParams.get("control_id")).toBe(
      CONTROL_ID,
    );

    await expect(page.getByTestId("control-state-result-value")).toHaveText(
      "Pass",
    );
    await expect(page.getByTestId("exceptions-empty")).toBeVisible();
    await expect(page.getByTestId("exception-row")).toHaveCount(0);
    await expect(page.getByText("Other control's waiver")).toHaveCount(0);
    await expect(page.getByTestId("control-strip-exception")).toHaveCount(0);
    await expect(page.getByTestId("coverage-excepted")).toHaveCount(0);
  });

  test("a failing section shows its own error and the rest renders", async ({
    authedPage: page,
  }) => {
    await mockPage(page, {
      approver: true,
      failEvidence: true,
      failRisks: true,
    });
    await openPage(page);

    await expect(page.getByTestId("evidence-stream-error")).toBeVisible();
    await expect(page.getByTestId("risks-error")).toBeVisible();

    // The other sections still stand.
    await expect(page.getByTestId("coverage-row")).toHaveCount(2);
    await expect(page.getByTestId("exception-row")).toHaveCount(1);
    await expect(page.getByTestId("linked-action-plan")).toHaveCount(1);
    await expect(page.getByTestId("policy-row")).toHaveCount(1);
    await expect(page.getByTestId("control-owner")).toHaveText("Olive Owner");
  });
});
