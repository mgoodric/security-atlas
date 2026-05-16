// Slice 042 — Playwright E2E for the audit workspace flow.
//
// Runner status (post-slice-069, verified 2026-05-15 by slice 071 audit):
// Playwright IS installed in `web/` (`@playwright/test` in devDeps;
// `web/playwright.config.ts` present; CI runs `Frontend · Playwright
// e2e`). The job is currently quarantined per slice 079 because the
// five un-shimmed specs reference seed-data preconditions the
// docker-compose bring-up does not yet establish. Slice 082
// (`Playwright e2e seed-data harness`, status `not-ready`) is the fix;
// when it lands, the quarantine drops and the un-commented assertions
// below become the gate.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/audit-workspace.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_AUDITOR_BEARER carries a credential with an auditor_assignment
//     to a known, frozen AuditPeriod
//   - TEST_AUDITEE_BEARER carries a non-auditor credential in the same
//     tenant (for the P0-2 private-note assertion)
//   - the period has at least one control with evidence in-window

import { test } from "@playwright/test";

// Per the preamble above: assertions are deliberately commented pending
// the slice-082 seed-data harness. The test body is preserved verbatim
// as a reviewable contract.

test.describe("audit workspace", () => {
  test("AC-1: /audit lands the auditor in their assigned AuditPeriod", async () => {
    // 1. Sign in with the auditor bearer.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_AUDITOR_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit /audit.
    //    await page.goto("/audit");
    // 3. The AuditPeriod top-bar renders the assigned period name + window.
    //    await expect(page.getByTestId("audit-period-bar")).toBeVisible();
    //    await expect(page.getByTestId("audit-period-bar")).toContainText(
    //      "SOC 2 2026 Q2",
    //    );
    // 4. A frozen period shows the frozen badge (invariant 10 visual cue).
    //    await expect(page.getByTestId("period-frozen-badge")).toBeVisible();
  });

  test("AC-2: left nav lists controls in scope for the period", async () => {
    // The period-scoped control-list endpoint is a pending backend
    // slice; v1 seeds the nav from controls the auditor adds. The spec
    // asserts the documented empty-state, then the populated state.
    //    await page.goto("/audit");
    //    await expect(page.getByTestId("control-nav-empty")).toBeVisible();
    //    await page.fill('[data-testid="add-control-input"]', KNOWN_CONTROL_ID);
    //    await page.click('[data-testid="add-control-submit"]');
    //    await expect(page.getByTestId("control-nav-item")).toBeVisible();
  });

  test("AC-3: population summary + sample-pull + annotation", async () => {
    //    await page.goto(`/audit/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("panel-sampling")).toBeVisible();
    // Build a population.
    //    await page.fill('[data-testid="population-start"]', "2026-04-01");
    //    await page.fill('[data-testid="population-end"]', "2026-06-30");
    //    await page.click('[data-testid="population-create"]');
    //    await expect(page.getByTestId("population-row-count")).toBeVisible();
    // Pull a sample.
    //    await page.fill('[data-testid="sample-n-input"]', "5");
    //    await page.fill('[data-testid="sample-seed-input"]', "e2e-seed");
    //    await page.click('[data-testid="sample-pull-submit"]');
    //    await expect(page.getByTestId("sample-card")).toBeVisible();
    // Annotate the first evidence record.
    //    const ann = page.getByTestId("sample-annotation").first();
    //    await ann.locator('[data-testid="annotation-result-select"]').selectOption("passed");
    //    await ann.locator('[data-testid="annotation-notes-input"]').fill("looks good");
    //    await ann.locator('[data-testid="annotation-submit"]').click();
    //    await expect(ann.getByTestId("annotation-saved-result")).toContainText("passed");
  });

  test("AC-4: walkthrough recorder saves narrative + attachment", async () => {
    //    await page.goto(`/audit/${KNOWN_CONTROL_ID}`);
    //    await page.click('[data-testid="tab-walkthrough"]');
    //    await page.fill('[data-testid="walkthrough-narrative"]', "Owner demoed the IAM policy review.");
    //    await page.click('[data-testid="walkthrough-save"]');
    // On an admin/grc_engineer bearer the hash shows; on a pure auditor
    // bearer the recorder surfaces the documented 403 role-gap message.
    //    await expect(
    //      page.getByTestId("walkthrough-hash").or(page.getByTestId("walkthrough-error")),
    //    ).toBeVisible();
    // With a writer bearer, attach a file:
    //    await page.setInputFiles('[data-testid="walkthrough-file"]', "e2e/fixtures/screencap.png");
    //    await page.click('[data-testid="walkthrough-attach"]');
    //    await expect(page.getByTestId("walkthrough-attachment-count")).toBeVisible();
  });

  test("AC-5 + P0-2: comment thread distinguishes auditor vs auditee; private notes auditor-only", async () => {
    // As the auditor: post a shared comment and a private (auditor_only) one.
    //    await page.goto(`/audit/${KNOWN_CONTROL_ID}`);
    //    await page.click('[data-testid="tab-comments"]');
    //    await page.fill('[data-testid="comment-body"]', "Please attach the Q2 access review.");
    //    await page.selectOption('[data-testid="comment-visibility"]', "shared");
    //    await page.click('[data-testid="comment-submit"]');
    //    await expect(page.getByTestId("comment-shared-badge")).toBeVisible();
    //    await page.fill('[data-testid="comment-body"]', "Private: control owner was vague on rotation.");
    //    await page.selectOption('[data-testid="comment-visibility"]', "auditor_only");
    //    await page.click('[data-testid="comment-submit"]');
    //    await expect(page.getByTestId("comment-private-badge")).toBeVisible();
    // P0-2: sign in as the AUDITEE and confirm the private note is ABSENT.
    // The platform filters auditor_only notes server-side; the UI never
    // client-side-filters — so the private note simply never arrives.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_AUDITEE_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto(`/audit/${KNOWN_CONTROL_ID}`);  // (auditee path TBD)
    //    await page.click('[data-testid="tab-comments"]');
    //    await expect(page.getByTestId("comment-thread")).toContainText("Please attach the Q2 access review.");
    //    await expect(page.getByTestId("comment-thread")).not.toContainText("control owner was vague");
  });

  test("AC-6: sign-out clears session; re-sign-in resumes to assigned period", async () => {
    //    await page.goto("/audit");
    //    await expect(page.getByTestId("audit-period-bar")).toBeVisible();
    //    await page.click("text=Sign out");
    //    await page.waitForURL(/\/login/);
    // Visiting /audit while signed out redirects back to /login.
    //    await page.goto("/audit");
    //    await page.waitForURL(/\/login\?from=%2Faudit/);
    // Re-sign-in returns to /audit with the same assigned period.
    //    await page.fill('input[name="token"]', process.env.TEST_AUDITOR_BEARER!);
    //    await page.click("button[type=submit]");
    //    await page.goto("/audit");
    //    await expect(page.getByTestId("audit-period-bar")).toContainText("SOC 2 2026 Q2");
  });

  test("AC-7 + P0-3: tab between controls without losing in-progress annotations", async () => {
    // Open control A, draw a sample, start (do NOT save) an annotation.
    //    await page.goto(`/audit/${CONTROL_A}`);
    //    ... build population, pull sample ...
    //    const annA = page.getByTestId("sample-annotation").first();
    //    await annA.locator('[data-testid="annotation-result-select"]').selectOption("failed");
    //    await annA.locator('[data-testid="annotation-notes-input"]').fill("WIP: missing approver");
    //    await expect(annA.getByTestId("annotation-draft-indicator")).toBeVisible();
    // Switch to the Walkthrough tab and back — the draft must survive
    // (in-control tab switch: panels toggle via `hidden`, never unmount).
    //    await page.click('[data-testid="tab-walkthrough"]');
    //    await page.click('[data-testid="tab-sampling"]');
    //    await expect(annA.locator('[data-testid="annotation-notes-input"]')).toHaveValue("WIP: missing approver");
    // Navigate to control B and back to control A — the draft STILL
    // survives (cross-control: AnnotationDraftProvider lives in the
    // AuditShell, which Next keeps mounted across the sibling route).
    //    await page.click('[data-testid="add-control-input"]'); // add control B
    //    await page.fill('[data-testid="add-control-input"]', CONTROL_B);
    //    await page.click('[data-testid="add-control-submit"]');
    //    await page.goto(`/audit/${CONTROL_B}`);
    //    await page.goto(`/audit/${CONTROL_A}`);
    //    ... re-open the same sample ...
    //    await expect(page.getByTestId("annotation-notes-input").first()).toHaveValue("WIP: missing approver");
  });

  test("P0-1: workspace never fetches data outside the assigned period", async () => {
    // Static guarantee, asserted by inspection + network log:
    //   - the page resolves the period ONLY via /v1/me/audit-period
    //   - no URL accepts a period id; /audit/[controlId] takes a control
    //     id only
    //   - every BFF route forwards the caller's bearer; the platform's
    //     RLS + auditor_assignments scope the response
    // The network-log assertion: no request to /v1/audit-periods/{otherId}
    // or /v1/controls/:id/state (the live path) is ever made.
    //    const requests: string[] = [];
    //    page.on("request", (r) => requests.push(r.url()));
    //    await page.goto(`/audit/${KNOWN_CONTROL_ID}`);
    //    ... exercise the workspace ...
    //    expect(requests.some((u) => u.includes("/v1/controls/") && u.endsWith("/state"))).toBe(false);
  });
});
