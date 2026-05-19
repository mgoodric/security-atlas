// Slice 103 -- Playwright E2E for the /settings user-facing page.
// Slice 154 -- spec expanded with AC-7 through AC-10 (per-section
//              parity coverage) per the audit findings in
//              docs/audit-log/154-settings-page-audit-decisions.md.
//              Un-comment + seed fixture wiring deferred to spillover
//              slice #164 (slice 082 per-spec un-quarantine pattern).
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when
// that harness lands a per-spec un-comment slice, the bodies below
// become the gate. The test bodies are preserved verbatim as a
// reviewable contract per the slice 040 / 042 / 056 / 060 / 064 / 071
// / 094 / 098 / 099 / 100 / 101 / 102 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/settings.spec.ts
//
// Pre-conditions the seed-data harness (slice 164) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries an admin credential in a tenant with at
//     least one existing personal API token (drives AC-4 row check)
//   - the user row has at least one slice-130 `roles` entry beyond
//     `admin` so the tail badge AC-10 has data
//   - the user row has a non-default `time_zone` so the time-zone
//     `<select>` AC-8 assertion has a discriminator
//
// AC coverage targets:
//   AC-1 Profile section renders (display name, email, role badge)
//   AC-2 Theme picker persists across page reload
//   AC-3 Notification toggles persist server-side via /v1/me/preferences
//   AC-4 Token issuance shows plaintext exactly once, then never again
//   AC-5 Active sessions section renders (slice-108 backed)
//   AC-6 Admin cross-link visible for admin role
//   AC-7 Notifications section renders four event rows + 8 toggles (slice 154)
//   AC-8 Time-zone <select> reflects current value + PATCH wired (slice 154)
//   AC-9 API tokens section renders empty-state or row table (slice 154)
//   AC-10 Roles tail badge renders when slice-130 roles array is non-empty (slice 154)

import { test } from "@playwright/test";

test.describe("/settings user-facing page", () => {
  test("AC-1: profile section renders for any signed-in user", async () => {
    //    await page.goto("/settings");
    //    await expect(page.getByTestId("settings-section-profile")).toBeVisible();
    //    await expect(page.getByRole("heading", { name: /Profile/ })).toBeVisible();
  });

  test("AC-2: theme picker persists choice across reload", async () => {
    //    await page.goto("/settings");
    //    await page.getByTestId("settings-theme-option-dark").click();
    //    await page.reload();
    //    await expect(
    //      page.getByTestId("settings-theme-option-dark"),
    //    ).toHaveAttribute("data-selected", "true");
  });

  test("AC-3: notification toggle persists to localStorage", async () => {
    //    await page.goto("/settings");
    //    const toggle = page.getByTestId(
    //      "settings-notif-audit_period_assignment-email",
    //    );
    //    await toggle.uncheck();
    //    const stored = await page.evaluate(() =>
    //      window.localStorage.getItem(
    //        "security-atlas.settings.notif.audit_period_assignment.email",
    //      ),
    //    );
    //    expect(stored).toBe("false");
  });

  test("AC-4 + P0-A2: token issuance shows plaintext once then never re-displays it", async () => {
    //    await page.goto("/settings");
    //    await page.getByTestId("settings-token-issue-button").click();
    //    await page.getByTestId("settings-token-issue-form").waitFor();
    //    await page.getByRole("button", { name: /Issue token/ }).click();
    //
    //    // Callout appears with the plaintext.
    //    const callout = page.getByTestId("settings-fresh-token-callout");
    //    await callout.waitFor();
    //    const plaintext = await page
    //      .getByTestId("settings-fresh-token-bearer")
    //      .textContent();
    //    expect(plaintext).toBeTruthy();
    //    expect(plaintext!.length).toBeGreaterThan(20);
    //
    //    // Dismiss the callout -- plaintext MUST disappear from the DOM.
    //    await page.getByTestId("settings-fresh-token-dismiss").click();
    //    await expect(callout).not.toBeVisible();
    //
    //    // Reload the page -- plaintext MUST NOT reappear anywhere.
    //    await page.reload();
    //    await expect(callout).not.toBeVisible();
    //    const bodyText = await page.locator("body").textContent();
    //    expect(bodyText).not.toContain(plaintext!);
  });

  test("AC-5: active sessions placeholder visible (spillover banner)", async () => {
    //    await page.goto("/settings");
    //    await expect(
    //      page.getByTestId("settings-section-sessions"),
    //    ).toBeVisible();
    //    await expect(
    //      page.getByText(/Session management pending/),
    //    ).toBeVisible();
  });

  test("AC-6: admin cross-link visible only for admin role", async () => {
    //    // Admin bearer: cross-link visible.
    //    await page.goto("/settings");
    //    await expect(
    //      page.getByTestId("settings-admin-cross-link"),
    //    ).toBeVisible();
    //
    //    // Switch to a non-admin bearer (seed harness provides both).
    //    // The cross-link MUST be absent from the DOM.
    //    // ... sign-out / sign-in as the non-admin user ...
    //    // await page.goto("/settings");
    //    // await expect(
    //    //   page.getByTestId("settings-admin-cross-link"),
    //    // ).toHaveCount(0);
  });

  test("AC-7: notifications section renders four event rows with 8 toggles", async () => {
    // Slice 154: section coverage parity with the mockup. The four
    // NOTIF_EVENTS keys hard-coded in page.tsx must each render a row
    // with one in-app + one email toggle (8 inputs total). The toggle
    // states reflect the GET /v1/me/preferences response.
    //
    //    await page.goto("/settings");
    //    await expect(
    //      page.getByTestId("settings-section-notifications"),
    //    ).toBeVisible();
    //    for (const key of [
    //      "audit_period_assignment",
    //      "policy_ack_due",
    //      "risk_review_overdue",
    //      "control_drift",
    //    ]) {
    //      await expect(
    //        page.getByTestId(`settings-notif-row-${key}`),
    //      ).toBeVisible();
    //      await expect(
    //        page.getByTestId(`settings-notif-${key}-in-app`),
    //      ).toBeVisible();
    //      await expect(
    //        page.getByTestId(`settings-notif-${key}-email`),
    //      ).toBeVisible();
    //    }
    //    // Mockup F5 copy delta: the in-progress qualifier is present.
    //    await expect(
    //      page.getByTestId("settings-notif-row-audit_period_assignment"),
    //    ).toContainText("in-progress period");
  });

  test("AC-8: time-zone <select> reflects current value + PATCH wires", async () => {
    // Slice 154 F4: time zone editor binds to PATCH /v1/me. The select
    // ships nine curated zones plus an out-of-band synthetic option
    // when the backend reports a zone outside the list.
    //
    //    await page.goto("/settings");
    //    const tz = page.getByTestId("settings-profile-time-zone-select");
    //    await expect(tz).toBeVisible();
    //    // The select reflects the user's saved zone (seed fixture sets
    //    // it to America/New_York for the test user).
    //    await expect(tz).toHaveValue("America/New_York");
    //
    //    // Change to a different curated zone. The PATCH should fire
    //    // and the value should stick across a reload.
    //    await tz.selectOption("UTC");
    //    await page.waitForResponse(
    //      (r) => r.url().includes("/api/me") && r.request().method() === "PATCH",
    //    );
    //    await page.reload();
    //    await expect(
    //      page.getByTestId("settings-profile-time-zone-select"),
    //    ).toHaveValue("UTC");
  });

  test("AC-9: API tokens section renders empty-state or row table", async () => {
    // Slice 154 F8: rotate action is deferred (slice 163). The visible
    // contract here is the section's presence + correct empty-state OR
    // table render depending on whether the seed fixture inserts a
    // token row. The settings spillover fixture (slice 164) seeds two
    // rows so the table branch is exercised.
    //
    //    await page.goto("/settings");
    //    await expect(
    //      page.getByTestId("settings-section-tokens"),
    //    ).toBeVisible();
    //    // Either the empty-state copy OR a row is present — never neither.
    //    const rowCount = await page
    //      .getByTestId("settings-token-row")
    //      .count();
    //    if (rowCount === 0) {
    //      await expect(
    //        page.getByText(/No active tokens\./),
    //      ).toBeVisible();
    //    } else {
    //      await expect(
    //        page.getByRole("columnheader", { name: /Last 4/ }),
    //      ).toBeVisible();
    //    }
    //    // Issue button is present for admin (seed user is admin).
    //    await expect(
    //      page.getByTestId("settings-token-issue-button"),
    //    ).toBeVisible();
  });

  test("AC-10: roles tail badge renders when slice-130 roles array is non-empty", async () => {
    // Slice 154 F3: the multi-role tail ("+ grc_engineer + auditor")
    // renders next to the primary admin/user badge when /v1/me reports
    // additional roles. The seed fixture assigns at least one
    // secondary role to the test user so the tail must be visible.
    //
    //    await page.goto("/settings");
    //    await expect(
    //      page.getByTestId("settings-profile-roles"),
    //    ).toBeVisible();
    //    await expect(
    //      page.getByTestId("settings-profile-roles-tail"),
    //    ).toBeVisible();
    //    await expect(
    //      page.getByTestId("settings-profile-roles-tail"),
    //    ).toContainText("+ grc_engineer");
  });
});
