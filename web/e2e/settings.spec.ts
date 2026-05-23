// Slice 103 -- Playwright E2E for the /settings user-facing page.
// Slice 154 -- spec expanded with AC-7 through AC-10 (per-section
//              parity coverage) per the audit findings in
//              docs/audit-log/154-settings-page-audit-decisions.md.
//              Un-comment + seed fixture wiring deferred to spillover
//              slice #164 (slice 082 per-spec un-quarantine pattern).
// Slice 163 -- spec gains AC-11 (rotate-twice-in-a-row + chain) per
//              docs/issues/163-settings-api-tokens-rotate-action.md
//              AC-6.
// Slice 164 -- seed fixture (fixtures/e2e/settings.sql) + un-comment
//              of every AC body. See
//              docs/audit-log/164-settings-e2e-seed-decisions.md for
//              the JUDGMENT calls (notably D1: AC-3 contract reshaped
//              from a localStorage check to a server-round-trip check
//              now that slice 108 retired the localStorage fallback).
// Slice 203 -- spec gains AC-12 (selecting Dark actually themes the
//              UI). The visible regression slice 203 fixes: slice 170
//              persisted the picker choice + wrote <html data-theme>,
//              but no class="dark" was ever written, so the Tailwind
//              v4 `.dark { ... }` token block at globals.css:86+ never
//              activated. AC-12 binds the contract: select Dark,
//              assert the page's <body> computed background color
//              matches the dark-mode token (oklch(0.145 0 0)).
// Slice 248 -- spec gains AC-13 (page <title> is route-specific). The
//              sibling server-component layout at
//              `web/app/(authed)/settings/layout.tsx` declares the
//              metadata; this AC binds the visible-to-operator
//              contract that the browser tab reads "Settings ·
//              security-atlas" instead of inheriting the root
//              `<title>security-atlas</title>`.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/settings.spec.ts
//
// AC coverage targets:
//   AC-1 Profile section renders (display name, email, role badge)
//   AC-2 Theme picker persists across page reload
//   AC-3 Notification toggle persists server-side via /v1/me/preferences
//   AC-4 Token issuance shows plaintext exactly once, then never again
//   AC-5 Active sessions section renders (slice-108 backed)
//   AC-6 Admin cross-link visible for admin role
//   AC-7 Notifications section renders four event rows + 8 toggles (slice 154)
//   AC-8 Time-zone <select> reflects current value + PATCH wired (slice 154)
//   AC-9 API tokens section renders empty-state or row table (slice 154)
//   AC-10 Roles tail badge renders when slice-130 roles array is non-empty (slice 154)
//   AC-11 Rotate-twice-in-a-row chains predecessors + fresh secret per rotate (slice 163)
//   AC-13 Page <title> is route-specific via per-route layout metadata (slice 248)
//   (AC-12 lives in the AppearanceSection block alongside AC-2; the slice
//   203 author elected to keep the numbering inline rather than renumber.)

import { expect, test } from "./fixtures";

import { seedFromFixture } from "./seed";

test.beforeAll(() => {
  seedFromFixture("settings");
});

test.describe("/settings user-facing page", () => {
  test("AC-1: profile section renders for any signed-in user", async ({
    authedPage: page,
  }) => {
    // Slice 168 AC-1 fix (spec drift): the CardTitle "Profile" renders as a
    // shadcn `<div>` (web/components/ui/card.tsx:36 — `CardTitle` is
    // `React.ComponentProps<"div">`), not as an `<h*>` or `role="heading"`.
    // Playwright's `getByRole("heading", ...)` could never resolve to it.
    // Assert the rendered "Profile" label via `getByText` instead — same
    // intent (the section's title is visible), correct accessibility tree.
    await page.goto("/settings");
    await expect(page.getByTestId("settings-section-profile")).toBeVisible();
    await expect(
      page.getByTestId("settings-section-profile").getByText("Profile"),
    ).toBeVisible();
  });

  test("AC-2: theme picker persists choice across reload", async ({
    authedPage: page,
  }) => {
    await page.goto("/settings");
    await page.getByTestId("settings-theme-option-dark").click();
    await page.reload();
    await expect(
      page.getByTestId("settings-theme-option-dark"),
    ).toHaveAttribute("data-selected", "true");
  });

  test("AC-3: notification toggle persists server-side across reload (slice 164 D1)", async ({
    authedPage: page,
  }) => {
    // Slice 164 D1: the slice 154 commented body for AC-3 asserted the
    // toggle wrote to localStorage. Slice 108 retired that fallback —
    // toggles now PATCH /v1/me/preferences and the server is the source
    // of truth. This body asserts the server round-trip instead.
    //
    // The seed fixture starts with (audit_period_assignment, email) =
    // false; we flip it to true, wait for the BFF PATCH response, reload
    // the page, and assert the toggle is checked.
    //
    // Slice 171 (H4): the toggle is a React-controlled `<input
    // type="checkbox" checked={email}>` (page.tsx:670-675 / 679-685)
    // bound to TanStack-Query data. After click, React re-renders with
    // the still-stale `prefsQuery.data` and snaps the DOM `checked`
    // attribute back to `false` BEFORE the PATCH's `onSuccess` fires
    // the cache invalidation. Playwright's `locator.check()` auto-
    // verifies post-state ("Clicking the checkbox did not change its
    // state") and throws on this snap-back. `locator.click()` skips
    // that post-state assertion — the truthful invariant lives in the
    // server round-trip + post-reload `toBeChecked()` assertion below.
    // PR #368's CI log (run 26115121287 job 76802322769) confirms this
    // is the actual failure mode; the slice doc Narrative's "PATCH never
    // fires / 30s waitForResponse timeout" framing was a misdiagnosis.
    await page.goto("/settings");
    const toggle = page.getByTestId(
      "settings-notif-audit_period_assignment-email",
    );
    // Initial state from seed.sql is enabled=false.
    await expect(toggle).not.toBeChecked();
    const patchResponse = page.waitForResponse(
      (r) =>
        r.url().includes("/api/me/preferences") &&
        r.request().method() === "PATCH",
    );
    await toggle.click();
    await patchResponse;
    await page.reload();
    await expect(
      page.getByTestId("settings-notif-audit_period_assignment-email"),
    ).toBeChecked();
  });

  test("AC-4 + P0-A2: token issuance shows plaintext once then never re-displays it", async ({
    authedPage: page,
  }) => {
    // Slice 168 AC-4 fix (spec drift): two buttons on the page match
    // `/Issue token/` — the trigger button in the section header
    // (page.tsx:801 `data-testid="settings-token-issue-button"`) AND the
    // form submit button (page.tsx:1121-1123 — `<Button>Issue token</Button>`
    // / "Issuing..."). The unscoped `getByRole` raised
    //   `strict mode violation: getByRole('button', { name: /Issue token/ })
    //    resolved to 2 elements`
    // because by the time the click fires, both buttons are mounted (the
    // trigger button stays visible while the form is open per page.tsx:790).
    // Scope the role lookup to the form to disambiguate.
    await page.goto("/settings");
    await page.getByTestId("settings-token-issue-button").click();
    const issueForm = page.getByTestId("settings-token-issue-form");
    await issueForm.waitFor();
    await issueForm.getByRole("button", { name: /Issue token/ }).click();

    // Callout appears with the plaintext.
    const callout = page.getByTestId("settings-fresh-token-callout");
    await callout.waitFor();
    const plaintext = await page
      .getByTestId("settings-fresh-token-bearer")
      .textContent();
    expect(plaintext).toBeTruthy();
    expect(plaintext!.length).toBeGreaterThan(20);

    // Dismiss the callout -- plaintext MUST disappear from the DOM.
    await page.getByTestId("settings-fresh-token-dismiss").click();
    await expect(callout).not.toBeVisible();

    // Reload the page -- plaintext MUST NOT reappear anywhere.
    await page.reload();
    await expect(callout).not.toBeVisible();
    const bodyText = await page.locator("body").textContent();
    expect(bodyText).not.toContain(plaintext!);
  });

  test("AC-5: active sessions section renders (slice-108 backed; slice-162 metadata line)", async ({
    authedPage: page,
  }) => {
    // Slice 154 AC-5: section renders (slice-108-backed list of session rows).
    // Slice 162: when a session row carries the augmented fields
    // (user_agent, ip_address, geo_country, geo_city), the per-row
    // metadata line renders with `data-testid="settings-session-meta"`
    // containing the formatted "{ua} · {ip} · {city}, {country}" string.
    // Rows without those fields (pre-slice-162 sessions, or sessions
    // created by background flows with no http.Request) MUST NOT render
    // the metadata line element — honest empty, no fabricated placeholder
    // (P0-162-1).
    //
    // The slice 164 fixture seeds one augmented row (newer last_seen_at,
    // renders first) and one bare row (older last_seen_at, renders last).
    await page.goto("/settings");
    await expect(page.getByTestId("settings-section-sessions")).toBeVisible();
    const rows = page.getByTestId("settings-session-row");
    await expect(rows.first()).toBeVisible();
    // Slice 162: at least one row should carry the augmented metadata
    // line (the seed fixture inserts a row with UA + IP + geo).
    const metaRow = page.getByTestId("settings-session-meta").first();
    await expect(metaRow).toBeVisible();
    await expect(metaRow).toContainText("192.0.2.18");
    await expect(metaRow).toContainText("San Francisco");
    // Slice 162 P0-162-1: a session row WITHOUT augmented fields must
    // NOT render a metadata line (no placeholder text). The bare row
    // (older last_seen_at) sorts last under the handler's
    // ORDER BY last_seen_at DESC.
    const bareRow = rows.last();
    await expect(bareRow.getByTestId("settings-session-meta")).toHaveCount(0);
  });

  test("AC-6: admin cross-link visible only for admin role", async ({
    authedPage: page,
  }) => {
    // The seed bearer is is_admin=true (the slice 082 harness ensures
    // this) so the cross-link must render. The non-admin half of this
    // AC is exercised by the slice 154 audit decision in the upstream
    // BFF logic (`getSessionMe`) and a unit test — toggling user roles
    // mid-spec is out of scope for the Playwright surface.
    await page.goto("/settings");
    await expect(page.getByTestId("settings-admin-cross-link")).toBeVisible();
  });

  test("AC-7: notifications section renders four event rows with 8 toggles", async ({
    authedPage: page,
  }) => {
    // Slice 154: section coverage parity with the mockup. The four
    // NOTIF_EVENTS keys hard-coded in page.tsx must each render a row
    // with one in-app + one email toggle (8 inputs total). The toggle
    // states reflect the GET /v1/me/preferences response.
    await page.goto("/settings");
    await expect(
      page.getByTestId("settings-section-notifications"),
    ).toBeVisible();
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
    // Mockup F5 copy delta: the in-progress qualifier is present.
    await expect(
      page.getByTestId("settings-notif-row-audit_period_assignment"),
    ).toContainText("in-progress period");
  });

  test("AC-8: time-zone <select> reflects current value + PATCH wires", async ({
    authedPage: page,
  }) => {
    // Slice 154 F4: time zone editor binds to PATCH /v1/me. The select
    // ships nine curated zones plus an out-of-band synthetic option
    // when the backend reports a zone outside the list. The fixture
    // seeds the user's time_zone as 'America/New_York'.
    await page.goto("/settings");
    const tz = page.getByTestId("settings-profile-time-zone-select");
    await expect(tz).toBeVisible();
    await expect(tz).toHaveValue("America/New_York");

    // Change to a different curated zone. The PATCH should fire and
    // the value should stick across a reload.
    const patchResponse = page.waitForResponse(
      (r) =>
        r.url().includes("/api/me") &&
        !r.url().includes("/api/me/preferences") &&
        !r.url().includes("/api/me/sessions") &&
        r.request().method() === "PATCH",
    );
    await tz.selectOption("UTC");
    await patchResponse;
    await page.reload();
    await expect(
      page.getByTestId("settings-profile-time-zone-select"),
    ).toHaveValue("UTC");
  });

  test("AC-9: API tokens section renders empty-state or row table", async ({
    authedPage: page,
  }) => {
    // Slice 154 F8: the visible contract is the section's presence +
    // correct empty-state OR table render depending on whether the
    // seed fixture inserts a token row. The slice 164 fixture seeds
    // a predecessor + successor pair (plus the harness's own admin
    // bearer row) so the table branch is exercised.
    await page.goto("/settings");
    await expect(page.getByTestId("settings-section-tokens")).toBeVisible();
    const rowCount = await page.getByTestId("settings-token-row").count();
    expect(rowCount).toBeGreaterThan(0);
    await expect(
      page.getByRole("columnheader", { name: /Last 4/ }),
    ).toBeVisible();
    // Issue button is present for admin (seed user is admin).
    await expect(page.getByTestId("settings-token-issue-button")).toBeVisible();
  });

  test("AC-11 (slice 163): rotate-twice-in-a-row chains predecessors + fresh secret per rotate", async ({
    authedPage: page,
  }) => {
    // Slice 163 F8 spillover: the Rotate action on a personal API token row
    // mints a successor with a fresh bearer plaintext and leaves the
    // predecessor row visible with a muted "rotated -> ...last4" badge
    // (slice 062 D-062-3). A second rotate of the SUCCESSOR yields a new
    // bearer (distinct from the first rotate's) and chains the badge so
    // each rotate adds one more rotated-to link to the visible chain.
    //
    // P0-163-1 plaintext-once invariant: after each rotate's callout is
    // dismissed, the corresponding bearer MUST NOT appear anywhere in
    // the DOM, including on a reload.
    //
    // The slice 164 fixture seeds a starting predecessor → successor
    // chain (last4 'rt01' → 'rt02') so the table already has one
    // rotated-to link on mount. Each rotate this test performs adds
    // exactly one more link to the count.
    await page.goto("/settings");

    const rows = page.getByTestId("settings-token-row");
    await expect(rows.first()).toBeVisible();
    const rotatedLinks = page.getByTestId("settings-token-rotated-to-link");
    const baselineLinkCount = await rotatedLinks.count();

    // Locate the fixture's CURRENT (most-recent) seed token by its
    // deterministic last4 'rt02'. Rotating this row mints the next
    // successor in the chain (the first rotation extends the seed's
    // existing chain by one).
    const rotateTarget = rows.filter({ hasText: "rt02" }).first();
    await rotateTarget.getByTestId("settings-token-rotate-button").click();
    await page.getByTestId("settings-token-rotate-modal").waitFor();
    await page.getByRole("button", { name: /Rotate now/ }).click();

    // Callout shows the new bearer with rotate-flavour copy.
    const callout1 = page.getByTestId("settings-fresh-token-callout");
    await callout1.waitFor();
    await expect(page.getByTestId("settings-fresh-token-title")).toContainText(
      "rotated",
    );
    const bearer1 = await page
      .getByTestId("settings-fresh-token-bearer")
      .textContent();
    expect(bearer1).toBeTruthy();
    expect(bearer1!.length).toBeGreaterThan(20);

    // Dismiss; bearer1 must not appear anywhere in the DOM.
    await page.getByTestId("settings-fresh-token-dismiss").click();
    await expect(callout1).not.toBeVisible();
    let bodyText = await page.locator("body").textContent();
    expect(bodyText).not.toContain(bearer1!);

    // After rotation 1: link count increased by exactly 1 (rt02 now
    // points at the new successor).
    await expect(rotatedLinks).toHaveCount(baselineLinkCount + 1);

    // Rotation 2: rotate the NEW successor (whichever row in the table
    // does NOT yet carry a rotated-to link OR is not the seed bearer).
    // Simpler: the new successor is the only row whose last-4 matches
    // the meta line captured above. The meta string includes "…<last4>"
    // — extract by searching the cell for the new last4.
    //
    // The page's FreshTokenCallout rotated-meta shows "Predecessor
    // expires at <date> · …<predecessor_last4>" (see ROTATED_META in
    // page.tsx). The new bearer's last4 is the LAST 4 chars of the
    // bearer plaintext itself. Pull from the bearer instead.
    const successorLast4 = bearer1!.slice(-4);

    const rotateTarget2 = rows.filter({ hasText: successorLast4 }).first();
    await rotateTarget2.getByTestId("settings-token-rotate-button").click();
    await page.getByTestId("settings-token-rotate-modal").waitFor();
    await page.getByRole("button", { name: /Rotate now/ }).click();

    const bearer2 = await page
      .getByTestId("settings-fresh-token-bearer")
      .textContent();
    expect(bearer2).toBeTruthy();
    expect(bearer2).not.toEqual(bearer1);

    await page.getByTestId("settings-fresh-token-dismiss").click();
    bodyText = await page.locator("body").textContent();
    expect(bodyText).not.toContain(bearer2!);

    // After rotation 2: link count increased by exactly 2 from baseline
    // (rt02 → new1, new1 → new2).
    await expect(rotatedLinks).toHaveCount(baselineLinkCount + 2);

    // Reload: neither plaintext appears anywhere on the page.
    await page.reload();
    bodyText = await page.locator("body").textContent();
    expect(bodyText).not.toContain(bearer1!);
    expect(bodyText).not.toContain(bearer2!);
  });

  test("AC-10: roles tail badge renders when slice-130 roles array is non-empty", async ({
    authedPage: page,
  }) => {
    // Slice 154 F3: the multi-role tail ("+ grc_engineer + auditor")
    // renders next to the primary admin/user badge when /v1/me reports
    // additional roles. The fixture seeds two user_roles rows for the
    // demo user: admin (primary) and grc_engineer (drives the tail).
    await page.goto("/settings");
    await expect(page.getByTestId("settings-profile-roles")).toBeVisible();
    await expect(page.getByTestId("settings-profile-roles-tail")).toBeVisible();
    await expect(page.getByTestId("settings-profile-roles-tail")).toContainText(
      "+ grc_engineer",
    );
  });

  test("AC-12 (slice 203): selecting Dark applies CSS theming to the page", async ({
    authedPage: page,
  }) => {
    // Slice 203: the bug this regression guards is "selecting Dark
    // persists a choice + writes data-theme but the page never themes".
    // We bind the contract by clicking the Dark swatch, navigating to a
    // page that paints the canonical bg-background / text-foreground
    // tokens (the dashboard), and asserting the body's computed
    // background-color matches the dark-mode token (the .dark
    // { --background: oklch(0.145 0 0); ... } block in
    // web/app/globals.css:86+).
    //
    // The assertion shape: read window.getComputedStyle(body).
    // backgroundColor. Browsers normalize oklch() to rgb() in the
    // serialized CSS value, so we check for ANY of three shapes:
    //   1. `rgb(37, 37, 37)` -- Chromium's serialized form of
    //      oklch(0.145 0 0).
    //   2. `oklch(0.145 0 0)` -- if Chromium ever switches to native
    //      oklch serialization (currently only Firefox 113+ does this).
    //   3. A regex that anchors on "the color is not white". Since the
    //      light-mode token is oklch(1 0 0) -> rgb(255, 255, 255), and
    //      we only need to assert "the page themed", a clean negation
    //      against the light value is sufficient and avoids being
    //      brittle to browser-version-specific oklch precision.
    //
    // We follow the third path (negate against light). The light->dark
    // delta is the load-bearing observable; the exact rgb tuple matters
    // less than the "the page is no longer white" property.
    await page.goto("/settings");

    // Capture the LIGHT-mode body color before clicking. The picker
    // starts at "system"; in a CI Playwright run the headless Chromium
    // defaults to prefers-color-scheme: light unless explicitly
    // configured (web/playwright.config.ts does not override). The
    // class on <html> at this moment should be absent and the body
    // should compute to the light token.
    const lightBg = await page.evaluate(
      () => window.getComputedStyle(document.body).backgroundColor,
    );

    // Click "Dark" -- the picker's `choose("dark")` writes data-theme
    // AND adds class="dark" to <html>. The MutationObserver in
    // ThemeClassSync also fires; both code paths converge on the same
    // class state.
    await page.getByTestId("settings-theme-option-dark").click();

    // Assert the class is present on <html>.
    await expect(page.locator("html")).toHaveClass(/(^|\s)dark(\s|$)/);

    // Assert the body's computed background changed. This is the
    // visible-to-operator contract: the page actually re-themed.
    const darkBg = await page.evaluate(
      () => window.getComputedStyle(document.body).backgroundColor,
    );
    expect(darkBg).not.toBe(lightBg);

    // Navigate to the dashboard to prove the class persists across
    // route transitions (the inline early-paint script in
    // web/app/layout.tsx re-reads localStorage on the next page paint;
    // ThemeClassSync also re-converges).
    await page.goto("/");
    await expect(page.locator("html")).toHaveClass(/(^|\s)dark(\s|$)/);
    const darkBgOnDashboard = await page.evaluate(
      () => window.getComputedStyle(document.body).backgroundColor,
    );
    expect(darkBgOnDashboard).not.toBe(lightBg);

    // Reload: AC-3 (early-paint script) prevents the flash. We can't
    // directly assert "no flash" in Playwright (it would require a
    // frame-precise screenshot diff against a known-good baseline,
    // which is out of scope per P0-A5). What we CAN assert is that the
    // dark class is on <html> on the very first frame of the reloaded
    // page -- before any React effect has fired. Playwright's
    // page.reload() awaits networkIdle, so by the time the next
    // assertion runs, the inline script has already executed.
    await page.reload();
    await expect(page.locator("html")).toHaveClass(/(^|\s)dark(\s|$)/);

    // Restore light mode so the test does not leave dark-mode state
    // sticking to localStorage for subsequent specs (each Playwright
    // worker shares a browser context across tests in this file).
    await page.goto("/settings");
    await page.getByTestId("settings-theme-option-light").click();
    await expect(page.locator("html")).not.toHaveClass(/(^|\s)dark(\s|$)/);
  });

  test("AC-13: page <title> is route-specific (slice 248)", async ({
    authedPage: page,
  }) => {
    // Slice 248 -- the /settings page ships its own `<title>` via the
    // sibling server-component layout at web/app/(authed)/settings/layout.tsx
    // (page.tsx is a client component and cannot export metadata).
    // Without the layout, the page inherits the root
    // `<title>security-atlas</title>` from web/app/layout.tsx and is
    // indistinguishable from sibling routes in the browser tab.
    //
    // The assertion is intentionally exact-string -- the slice's
    // AC-1 binds the literal "Settings · security-atlas" (middle dot,
    // U+00B7) so any mojibake regression also trips this spec.
    await page.goto("/settings");
    await expect(page).toHaveTitle("Settings · security-atlas");
  });
});
