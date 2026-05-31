// Slice 250 -- Playwright E2E for the /settings Profile section
// rendering an honesty banner + degraded display when the caller is a
// credential bearer (bootstrap admin / API-key with no users row).
//
// Mirrors the slice 251 spec pattern: the default e2e fixture in
// `web/e2e/fixtures.ts` authenticates as the slice-082 admin seed user
// (which IS a real users row). To exercise the credential-bearer code
// path without expanding the fixture harness (out of scope per the
// slice's anti-criteria), this spec intercepts the relevant BFF routes
// via `page.route()` and rewrites the responses to the synthetic-
// credential shapes documented in:
//
//   - `internal/api/me/profile.go:269-282` (synthetic profile: empty
//     idp_subject + empty email + "API key <last4>" display_name)
//   - `internal/api/me/preferences.go:51,78` (404 with body
//     `{"error": "no preferences for this credential"}`)
//
// The pattern matches `web/e2e/admin-tenants.spec.ts`,
// `web/e2e/first-time-login.spec.ts`, and
// `web/e2e/settings-notifications-credential-bearer.spec.ts` -- BFF
// mocking is the canonical approach for credential-class assertions
// before a real credential fixture lands.
//
// AC coverage:
//   AC-2/AC-7: With a credential-bearer JWT, the Profile section
//              renders the banner; the degraded display omits the
//              time-zone editor and never surfaces the synthetic
//              two-letter initials.
//   AC-3:      With an OIDC-human-user JWT (default fixture flow), the
//              Profile section continues to render the slice-154 hero
//              block (initials + display_name + email + IdP caveat) +
//              the time-zone editor -- regression guard.
//
// P0-250-1: We never fabricate fields. The mocked /v1/me response
//           contains exactly what the platform returns for the live
//           credential bearer (empty email, empty idp_subject).
// P0-250-3: The OIDC-human-user branch is byte-identical to slice 154.
//           The second test case binds this contract.
// P0-250-5: No regression on the 11/11 settings.spec.ts ACs. The new
//           spec is a sibling file; settings.spec.ts is unaltered.

import { fulfillFromGolden } from "./test-utils/fulfill-from-golden";

import { expect, test } from "./fixtures";

import {
  PROFILE_CREDENTIAL_BANNER_BODY,
  PROFILE_CREDENTIAL_BANNER_TITLE,
} from "../app/(authed)/settings/profile-bearer-display";

// Slice 394: the synthetic-credential `/v1/me` body now loads from the
// recorded contract golden (`me.golden.json` variant `synthetic_admin`),
// so the e2e mock cannot drift from the real handler's profile wire shape
// (slice 334 P-1 / ADR-0007). The golden supplies the shape-complete base
// (the always-present `roles: []`, `is_admin`, `tenant_role`, the empty
// email/idp_subject the credential path emits, `time_zone: null`). Two
// fields are OVERRIDDEN per this spec's load-bearing assertions (AC-3
// override escape hatch — decisions log D3):
//   * display_name — the visible test asserts the formatted credential
//     label ends in "…1f3a"; the golden records "API key ad01".
//   * owner_roles  — this spec exercised the admin owner-role path.
const ME_OVERRIDE = {
  display_name: "API key 1f3a",
  owner_roles: ["admin"],
};

test.describe("/settings Profile section -- credential bearer", () => {
  test("renders the honesty banner + degraded display for a credential bearer (AC-2 + AC-7)", async ({
    authedPage: page,
  }) => {
    // Intercept the BFF routes BEFORE navigating so the page's first
    // /v1/me round-trip resolves to the synthetic shape.

    // 1. /api/me -- return the synthetic-credential profile shape so
    //    the front-end's `isCredentialBearer` predicate returns true.
    await page.route("**/api/me", async (route) => {
      // Pass through PATCH / DELETE if any test ever fires one; this
      // spec only navigates so GET is the only verb we expect, but
      // let's not strip unrelated traffic.
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await fulfillFromGolden(route, "me", "synthetic_admin", {
        override: ME_OVERRIDE,
      });
    });

    // 2. /api/me/preferences -- return the 404 + documented error
    //    string the platform emits for credential bearers. This keeps
    //    the Notifications section happy via slice 251's banner branch
    //    (so the page renders end-to-end without a separate failure).
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

    // 3. /api/me/sessions -- empty list keeps the Sessions section
    //    settled without an error. Not load-bearing for this spec.
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

    const section = page.getByTestId("settings-section-profile");
    await expect(section).toBeVisible();

    // The banner is rendered with the exact slice-250 title + body.
    await expect(
      page.getByTestId("settings-profile-credential-banner"),
    ).toBeVisible();
    await expect(
      page.getByTestId("settings-profile-credential-banner-title"),
    ).toHaveText(PROFILE_CREDENTIAL_BANNER_TITLE);
    await expect(
      page.getByTestId("settings-profile-credential-banner-body"),
    ).toHaveText(PROFILE_CREDENTIAL_BANNER_BODY);

    // Hero block: the credential badge is rendered (NOT the
    // initials avatar). The label shows the formatted credential
    // identifier, not the raw "API key 1f3a" platform string.
    await expect(
      page.getByTestId("settings-profile-credential-badge"),
    ).toBeVisible();
    await expect(
      page.getByTestId("settings-profile-credential-label"),
    ).toContainText("API key …1f3a");

    // Initials avatar MUST NOT be rendered for a credential -- the
    // slice-154 hero block's initials would say "AP" for "API key"
    // (the visible-regression observation that motivated this slice).
    await expect(page.getByTestId("settings-profile-initials")).toHaveCount(0);

    // Email row uses the credential-specific copy.
    await expect(
      page.getByTestId("settings-profile-credential-email"),
    ).toContainText("not applicable");
    // The IdP caveat MUST NOT appear for a credential -- it would lie
    // about which managed-by relationship governs the field.
    const sectionText = (await section.textContent()) ?? "";
    expect(sectionText).not.toContain("managed by IdP");
    // The raw platform error string (slice 251 P0-251-2 parity) MUST
    // NOT bleed into the Profile section either.
    expect(sectionText).not.toContain("no preferences for this credential");

    // Time-zone editor MUST NOT render for a credential (PATCH /v1/me
    // would 404; showing a control that fails on submit is dishonest).
    await expect(
      page.getByTestId("settings-profile-time-zone-select"),
    ).toHaveCount(0);

    // Tenant-role badge stays -- the role IS authoritative for the
    // credential's authz. The seed shape sets is_admin = true.
    await expect(page.getByTestId("settings-profile-role-admin")).toBeVisible();
  });

  test("OIDC-human-user JWT still renders the slice-154 hero block + time-zone editor (AC-3 regression guard)", async ({
    authedPage: page,
  }) => {
    // No route interception -- the default fixture's seed user IS a
    // real OIDC-backed users row, so the section renders the canonical
    // slice-154 layout. This duplicates settings.spec.ts AC-1 / AC-8
    // intentionally so the slice 250 contract ("credential branch is
    // added WITHOUT regressing the OIDC branch") is bound in one
    // place.
    await page.goto("/settings");

    const section = page.getByTestId("settings-section-profile");
    await expect(section).toBeVisible();

    // The slice 250 banner MUST NOT appear for a real OIDC user.
    await expect(
      page.getByTestId("settings-profile-credential-banner"),
    ).toHaveCount(0);
    await expect(
      page.getByTestId("settings-profile-credential-badge"),
    ).toHaveCount(0);

    // The slice 154 hero block IS rendered (initials avatar present).
    await expect(page.getByTestId("settings-profile-initials")).toBeVisible();

    // The time-zone editor IS rendered (real users can patch their
    // time zone; PATCH /v1/me succeeds for them).
    await expect(
      page.getByTestId("settings-profile-time-zone-select"),
    ).toBeVisible();

    // The "(read-only · managed by IdP)" caveat IS present on the
    // email row -- this is the slice 154 wording that the credential
    // branch suppresses.
    await expect(section).toContainText("managed by IdP");
  });
});
