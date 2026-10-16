import { test, expect } from "@playwright/test";

test.describe("live installation smoke test", () => {
  test("successful login and dashboard layout validation", async ({ page }) => {
    const email = process.env.TEST_USER_EMAIL;
    const password = process.env.TEST_USER_PASSWORD;

    if (!email || !password) {
      throw new Error("TEST_USER_EMAIL and TEST_USER_PASSWORD must be set for live-smoke test");
    }

    console.log(`Live Smoke: Navigating to login page...`);
    await page.goto("/login");

    // Assert that the email/password sign-in form is visible
    const emailInput = page.locator('input[name="email"]');
    const passwordInput = page.locator('input[name="password"]');
    const submitButton = page.getByRole("button", { name: "Sign in" }).first();

    await expect(emailInput).toBeVisible({ timeout: 10000 });
    await expect(passwordInput).toBeVisible();
    await expect(submitButton).toBeVisible();

    console.log(`Live Smoke: Submitting login credentials...`);
    await emailInput.fill(email);
    await passwordInput.fill(password);
    
    // Click and wait for dashboard navigation to settle
    await Promise.all([
      page.waitForURL(/\/dashboard/, { timeout: 15000 }),
      submitButton.click(),
    ]);

    console.log(`Live Smoke: Logged in. Validating dashboard panels...`);
    // Assert program dashboard container is visible
    await expect(page.getByTestId("program-dashboard")).toBeVisible({ timeout: 10000 });

    // Validate main dashboard elements (some might render empty states or data depending on setup)
    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();

    console.log(`Live Smoke: Verifying logout...`);
    // Clear session cookies and navigate back to verify the login redirect
    await page.context().clearCookies();
    await page.goto("/dashboard");
    await page.waitForURL(/\/login/, { timeout: 5000 });
    expect(page.url()).toContain("/login");
  });
});
