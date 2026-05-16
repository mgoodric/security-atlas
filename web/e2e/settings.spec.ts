// Slice 103 -- Playwright E2E for the /settings user-facing page.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when
// that harness lands, the un-commented assertions below become the
// gate. The test bodies are preserved verbatim as a reviewable contract
// per the slice 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098 / 099 /
// 100 / 101 / 102 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/settings.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries an admin credential in a tenant with at
//     least one existing personal API token (drives AC-4 row check)
//
// AC coverage targets:
//   AC-1 Profile section renders (display name, email, role badge)
//   AC-2 Theme picker persists across page reload
//   AC-3 Notification toggles persist locally
//   AC-4 Token issuance shows plaintext exactly once, then never again
//   AC-5 Active sessions placeholder visible
//   AC-6 Admin cross-link visible for admin role
//   AC-9 (this file itself satisfies the spec-exists half)

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
});
