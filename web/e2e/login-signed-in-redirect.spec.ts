// #2062 — a signed-in user who opens /login is sent on: to the dashboard,
// or to the `from` target when it is a safe in-app path. `from` flows
// through safeRedirectTarget (slice 086), so an attacker-supplied URL still
// lands on /dashboard (last test). The same guard on the signIn action's
// post-sign-in redirect is auth-open-redirect.spec.ts, which runs where the
// token form is offered.

import { test as authed, expect } from "./fixtures";

authed.describe("#2062 — signed-in visitor to /login", () => {
  authed("lands on the dashboard", async ({ authedPage }) => {
    await authedPage.goto("/login");
    await authedPage.waitForURL((url) => !url.pathname.startsWith("/login"), {
      timeout: 10_000,
    });
    expect(new URL(authedPage.url()).pathname.startsWith("/dashboard")).toBe(
      true,
    );
  });

  authed("lands on a safe ?from= target", async ({ authedPage }) => {
    await authedPage.goto("/login?from=/risks");
    await authedPage.waitForURL((url) => !url.pathname.startsWith("/login"), {
      timeout: 10_000,
    });
    expect(new URL(authedPage.url()).pathname).toBe("/risks");
  });

  authed(
    "an attacker ?from= lands on /dashboard, not the attacker URL",
    async ({ authedPage }) => {
      await authedPage.goto("/login?from=https://evil.example.com/phish");
      await authedPage.waitForURL((url) => !url.pathname.startsWith("/login"), {
        timeout: 10_000,
      });
      const final = new URL(authedPage.url());
      expect(final.host).not.toBe("evil.example.com");
      expect(final.pathname.startsWith("/dashboard")).toBe(true);
    },
  );
});
