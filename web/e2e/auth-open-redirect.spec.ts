// Slice 086 — Playwright E2E asserting the open-redirect fix end-to-end.
//
// The 2026-Q2 security audit flagged HIGH: `web/app/login/actions.ts`
// passed the `?from=` form field straight to Next.js `redirect()`,
// enabling phishing pivots like
// `/login?from=https://evil.example.com/phish`. Slice 086 routes both
// call sites through `safeRedirectTarget` (web/lib/safe-redirect.ts),
// which falls back to `/dashboard` for any non-safe target.
//
// This spec is the live verification (vs. the unit test in
// `web/lib/safe-redirect.test.ts`). It drives the actual sign-in form
// on `/login`, supplies an attacker-controlled `from` query param, and
// asserts the post-sign-in URL is `/dashboard` — not the attacker URL.
//
// Quarantine note (slice 079): this spec runs under the Frontend ·
// Playwright e2e job whose CI step is `continue-on-error: true` at the
// job level. A spec failure surfaces in the HTML report + traces
// artifacts but does NOT block the PR. The unit test is the
// always-required gate; this spec is the belt-and-suspenders
// integration verification.
//
// Un-quarantined by slice 351 (AC-4, disposition (a)).
//
// Former skip condition: a file-scope `test.skip(!HAS_BEARER, ...)`
// where `HAS_BEARER = !!process.env.TEST_BEARER` was evaluated at
// module-load. That guard dated to the pre-slice-201 era when the
// harness provided no bearer and `authedPage` would throw. Slice 201's
// `globalSetup` (web/e2e/global-setup.ts) now mints a JWT via the
// env-gated `POST /v1/test/issue-jwt` and writes it into
// `process.env.TEST_BEARER` BEFORE any worker imports a spec — so the
// guard was vestigial and always skipped the spec in environments where
// it should have RUN. The spec body itself was fixed by slice 161 (the
// racy `waitForURL` → settled-pathname wait; see
// docs/audit-log/161-playwright-auth-open-redirect-spec-drift-decisions.md).
// Removing the guard makes this an honest live end-to-end security
// regression gate against the real login form. If the bearer is somehow
// absent, the `authedPage` fixture throws a clear error — which is the
// correct fail-loud behaviour for a required gate, not a silent skip.
//
// Hard rule (P0-A9 from slice 069's fixtures): no vendor-prefixed token
// strings in this file. GitGuardian scans test files too.
//
// #2062 — /login no longer renders a form for a signed-in visitor: it
// redirects them, and the developer token card is offered only with
// ATLAS_WEB_DEV_TOKEN_SIGNIN=true (or during first install). So this
// signIn-action check now drives the token form from a SIGNED-OUT page and
// runs only where the card is offered: the production-build CI leg sets
// the flag and runs this spec. login-signed-in-redirect.spec.ts covers the
// same attacker `from` on the signed-in redirect, on every leg.

import { expect, test } from "@playwright/test";

const flagOn = process.env.ATLAS_WEB_DEV_TOKEN_SIGNIN === "true";

test.describe("open-redirect defense on signIn", () => {
  test.skip(
    !flagOn,
    "#2062: the token form is only offered with ATLAS_WEB_DEV_TOKEN_SIGNIN=true",
  );

  test("sign-in with attacker ?from= lands on /dashboard, not attacker URL", async ({
    page,
  }) => {
    // Drive the login URL with an attacker-controlled `from` value. The
    // login form preserves `from` from the query string into a hidden
    // input, so a successful sign-in would otherwise redirect there. With
    // slice 086's helper in place, the redirect lands on /dashboard.
    await page.goto("/login?from=https://evil.example.com/phish");

    const tokenInput = page.locator('input[name="token"]');
    await tokenInput.waitFor({ state: "visible", timeout: 5_000 });
    await tokenInput.fill(process.env.TEST_BEARER ?? "");
    // #1756: the email/password form is always on /login too, so scope
    // the submit to the bearer-token form.
    await page
      .locator("form", { has: tokenInput })
      .getByRole("button", { name: /sign in/i })
      .click();

    // Slice 161: wait until the pathname is no longer `/login` (the
    // post-sign-in transition), then assert on the settled URL.
    await page.waitForURL((url) => !url.pathname.startsWith("/login"), {
      timeout: 5_000,
    });

    const final = new URL(page.url());
    expect(final.host).not.toBe("evil.example.com");
    expect(final.pathname.startsWith("/dashboard")).toBe(true);
  });
});
