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
// Skip condition: when `TEST_BEARER` is not set the worker-scoped
// authedPage fixture would throw. To keep the spec skip-rather-than-
// error in local dev runs, we guard on the env var at file scope —
// matching the slice-073 first-time-login.spec.ts pattern of an
// explicit skip when preconditions aren't available.
//
// Hard rule (P0-A9 from slice 069's fixtures): no vendor-prefixed token
// strings in this file. GitGuardian scans test files too.

import { expect, test } from "@playwright/test";

import { test as authed } from "./fixtures";

const HAS_BEARER = !!process.env.TEST_BEARER;

test.describe("open-redirect defense on signIn", () => {
  test.skip(
    !HAS_BEARER,
    "TEST_BEARER not set — skip until the seed harness provides one",
  );

  authed(
    "sign-in with attacker ?from= lands on /dashboard, not attacker URL",
    async ({ authedPage }) => {
      // Drive the login URL with an attacker-controlled `from` value.
      // The login form preserves `from` from the query string into a
      // hidden input, so a successful sign-in would otherwise redirect
      // there. With slice 086's helper in place, the redirect lands on
      // /dashboard instead.
      await authedPage.goto("/login?from=https://evil.example.com/phish");

      // The fixture already injected the session cookie, but `/login`
      // still renders the form (it has no signed-in redirect on the
      // server side). Fill + submit. The server action runs, the
      // helper rejects the attacker URL, and the post-sign-in redirect
      // targets `/dashboard`.
      const tokenInput = authedPage.locator('input[name="token"]');
      await tokenInput.waitFor({ state: "visible", timeout: 5_000 });
      await tokenInput.fill(process.env.TEST_BEARER ?? "");
      await authedPage.getByRole("button", { name: /sign in/i }).click();

      // Wait for the redirect to settle off `/login`. The prior shape —
      // `(url) => url.origin === new URL(authedPage.url()).origin` —
      // was a no-op: `authedPage.url()` returns the current page URL
      // at evaluation time, so the predicate compared the candidate
      // URL's origin against itself and resolved immediately. That
      // meant `authedPage.url()` at line `final = new URL(...)` below
      // was read mid-flight, capturing the pre-redirect `/login` URL
      // even though the server action HAD redirected to `/dashboard`.
      // The page snapshot at assertion-failure time showed the
      // dashboard fully rendered while `final.pathname` was still
      // `/login` — classic racy-wait. See slice 161 decisions log.
      //
      // The fix: wait until pathname is no longer `/login`. That's the
      // post-sign-in transition we actually care about. The host-and-
      // pathname assertions below then run on the settled URL.
      await authedPage.waitForURL((url) => !url.pathname.startsWith("/login"), {
        timeout: 5_000,
      });

      const final = new URL(authedPage.url());
      expect(final.host).not.toBe("evil.example.com");
      // The fallback destination is /dashboard. We accept either an
      // exact match or any path that starts with /dashboard (e.g. if
      // the dashboard route ever introduces a sub-route).
      expect(final.pathname.startsWith("/dashboard")).toBe(true);
    },
  );
});
