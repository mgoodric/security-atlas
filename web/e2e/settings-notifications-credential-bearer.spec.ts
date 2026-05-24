// Slice 251 -- Playwright E2E for the /settings Notifications section
// rendering an honest-disclosure banner when the caller is a credential
// bearer (bootstrap admin / API-key with no users row).
//
// The default e2e fixture in `web/e2e/fixtures.ts` authenticates as the
// slice-082 admin seed user, which IS a real users row. To exercise
// the credential-bearer code path without expanding the fixture
// harness (out of scope per the slice's anti-criteria), this spec
// intercepts the relevant BFF routes via `page.route()` and rewrites
// the responses to the synthetic-credential shapes documented in:
//
//   - `internal/api/me/profile.go:269-282` (synthetic profile: empty
//     idp_subject + empty email + "API key <last4>" display_name)
//   - `internal/api/me/preferences.go:51,78` (404 with body
//     `{"error": "no preferences for this credential"}`)
//
// The pattern matches `web/e2e/admin-tenants.spec.ts` and
// `web/e2e/first-time-login.spec.ts` -- BFF mocking is the canonical
// approach for credential-class assertions before a real credential
// fixture lands.
//
// AC coverage:
//   AC-2/AC-4: With a credential-bearer JWT, the Notifications section
//              renders the banner; the four event rows are NOT
//              rendered; no raw error string surfaces.
//   AC-3:      With an OIDC-human-user JWT (default fixture flow), the
//              section continues to render the four event rows × two
//              channels -- regression guard.
//
// P0-251-3 (do NOT touch the existing OIDC flow): we never alter the
// real BFF behaviour. The mocks only short-circuit the responses; the
// existing settings.spec.ts coverage stays the source of truth for the
// happy path.

import { expect, test } from "./fixtures";

import {
  CREDENTIAL_BEARER_BANNER_BODY,
  CREDENTIAL_BEARER_BANNER_TITLE,
} from "../app/(authed)/settings/notif-bearer-mode";

// Synthetic-profile shape returned by /v1/me for credential bearers.
// Mirrors `internal/api/me/profile.go:269-282`.
const SYNTHETIC_CREDENTIAL_PROFILE = {
  // The credential's own user_id (typically NIL UUID for bootstrap; a
  // synthetic non-UUID string for API-key with no users row backing).
  // The exact value is not load-bearing for the UI test.
  user_id: "00000000-0000-0000-0000-000000000000",
  tenant_id: "00000000-0000-0000-0000-000000000001",
  display_name: "API key 1f3a",
  email: "",
  idp_subject: "",
  tenant_role: "admin",
  time_zone: null,
  is_admin: true,
  owner_roles: [],
  roles: [],
};

test.describe("/settings Notifications section -- credential bearer", () => {
  test("renders the honest-disclosure banner for a credential bearer (AC-2 + AC-4)", async ({
    authedPage: page,
  }) => {
    // Intercept the BFF routes BEFORE navigating.
    //
    // 1. /api/me -- return the synthetic-credential profile shape so
    //    the front-end's `notificationsRenderMode` helper resolves to
    //    "credential".
    await page.route("**/api/me", async (route) => {
      // Pass through PATCH / DELETE if any test ever fires one; this
      // spec only navigates so GET is the only verb we expect, but
      // let's not strip unrelated traffic.
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(SYNTHETIC_CREDENTIAL_PROFILE),
      });
    });

    // 2. /api/me/preferences -- return the 404 + documented error
    //    string the platform emits for credential bearers. This
    //    corroborates the profile signal AND exercises the helper's
    //    substring-match path even if the profile signal alone is
    //    sufficient.
    await page.route("**/api/me/preferences", async (route) => {
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({
          error: "no preferences for this credential",
        }),
      });
    });

    // 3. /api/me/sessions -- the SessionsSection also reads /v1/me/sessions.
    //    Return an empty list so the section settles without an error.
    //    (Not load-bearing for THIS spec's AC; just keeps the page quiet.)
    await page.route("**/api/me/sessions", async (route) => {
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ sessions: [], count: 0 }),
      });
    });

    await page.goto("/settings");

    // The Notifications section is visible (the page did not crash).
    await expect(
      page.getByTestId("settings-section-notifications"),
    ).toBeVisible();

    // The banner is rendered with the exact slice-251 title + body.
    await expect(
      page.getByTestId("settings-notif-credential-banner"),
    ).toBeVisible();
    await expect(
      page.getByTestId("settings-notif-credential-banner-title"),
    ).toHaveText(CREDENTIAL_BEARER_BANNER_TITLE);
    await expect(
      page.getByTestId("settings-notif-credential-banner-body"),
    ).toHaveText(CREDENTIAL_BEARER_BANNER_BODY);

    // The four event rows are NOT rendered (the banner replaced them).
    for (const key of [
      "audit_period_assignment",
      "policy_ack_due",
      "risk_review_overdue",
      "control_drift",
    ]) {
      await expect(page.getByTestId(`settings-notif-row-${key}`)).toHaveCount(
        0,
      );
      await expect(
        page.getByTestId(`settings-notif-${key}-in-app`),
      ).toHaveCount(0);
      await expect(page.getByTestId(`settings-notif-${key}-email`)).toHaveCount(
        0,
      );
    }

    // The raw platform error string MUST NOT appear in the section
    // (P0-251-2: API stays honest; UI translates). We assert via the
    // section's text content, not the whole page, because the rest of
    // /settings (e.g. token rows) may legitimately contain unrelated
    // copy.
    const sectionText = await page
      .getByTestId("settings-section-notifications")
      .textContent();
    expect(sectionText ?? "").not.toContain(
      "no preferences for this credential",
    );

    // No skeleton lingers (the slice's narrative observation -- the
    // pre-fix SSR shipped a skeleton that the post-hydration path
    // never replaced). Confirm the banner replaced the skeleton.
    await expect(
      page
        .getByTestId("settings-section-notifications")
        .locator('[data-slot="skeleton"]'),
    ).toHaveCount(0);
  });

  test("OIDC-human-user JWT still renders the four event rows (AC-3 regression guard)", async ({
    authedPage: page,
  }) => {
    // No route interception here -- the default fixture's seed user
    // IS a real OIDC-backed users row, so the section should render
    // the canonical four event rows × two channels. This duplicates
    // the existing settings.spec.ts AC-7 assertion intentionally:
    // slice 251's contract is "credential branch is added WITHOUT
    // regressing the OIDC branch", and binding the regression to a
    // sibling test makes the contract visible in one place.
    await page.goto("/settings");
    await expect(
      page.getByTestId("settings-section-notifications"),
    ).toBeVisible();
    // Banner MUST NOT appear for a real OIDC user.
    await expect(
      page.getByTestId("settings-notif-credential-banner"),
    ).toHaveCount(0);
    // The four event rows render with both channel toggles each.
    for (const key of [
      "audit_period_assignment",
      "policy_ack_due",
      "risk_review_overdue",
      "control_drift",
    ]) {
      await expect(page.getByTestId(`settings-notif-row-${key}`)).toBeVisible();
      await expect(
        page.getByTestId(`settings-notif-${key}-in-app`),
      ).toBeVisible();
      await expect(
        page.getByTestId(`settings-notif-${key}-email`),
      ).toBeVisible();
    }
  });
});
