// #2062 — /login offers the developer (paste-a-bearer-token) sign-in only
// when the deployment opts in with ATLAS_WEB_DEV_TOKEN_SIGNIN=true, or
// while the install is in first-install state (bootstrap token, or the
// --reset-bootstrap recovery key).
//
// Both settings are covered in CI by running this spec in two legs:
//
//   * `Frontend · Playwright e2e` starts the web server WITHOUT the flag,
//     so the card must be absent (the default).
//   * `Frontend · Playwright e2e (production build)` sets
//     ATLAS_WEB_DEV_TOKEN_SIGNIN=true on the web server, so the card must
//     render.
//
// The spec reads the same env var as the server it runs against: both are
// started from the same job env. The first-install branch is decided from
// a server-side install-state read that page.route() cannot mock (it is
// not browser traffic), so the spec asks the platform for the real state
// and expects the card when either branch holds. The branch itself is
// covered in vitest (lib/auth/dev-token-signin.test.ts and
// app/login/actions.test.ts).

import { expect, test } from "@playwright/test";

const flagOn = process.env.ATLAS_WEB_DEV_TOKEN_SIGNIN === "true";
const atlasURL = process.env.ATLAS_HTTP_URL || "http://localhost:8080";

async function firstInstall(
  request: import("@playwright/test").APIRequestContext,
): Promise<boolean> {
  // Mirrors the server: an unreadable install-state is "not first install".
  try {
    const res = await request.get(`${atlasURL}/v1/install-state`);
    if (!res.ok()) return false;
    const body = (await res.json()) as { first_install?: boolean };
    return body.first_install === true;
  } catch {
    return false;
  }
}

test.describe("#2062 — developer token sign-in card", () => {
  test("is absent by default, present with ATLAS_WEB_DEV_TOKEN_SIGNIN=true", async ({
    page,
    request,
  }) => {
    const expectCard = flagOn || (await firstInstall(request));

    await page.goto("/login");
    // The email/password form always renders.
    await expect(page.getByLabel("Email")).toBeVisible();

    const card = page.getByTestId("dev-token-signin-card");
    if (expectCard) {
      await expect(card).toBeVisible();
      await expect(page.getByLabel("Bearer token")).toBeVisible();
    } else {
      await expect(card).toHaveCount(0);
      await expect(page.getByLabel("Bearer token")).toHaveCount(0);
    }
  });
});
