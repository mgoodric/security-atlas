// Slice 075 — Playwright E2E for the logo across the integration surfaces.
//
// AC-8: a Playwright spec asserts the logo <img> (or <picture>) renders
// on the login + dashboard layouts, at the expected viewport, with the
// <source media="prefers-color-scheme: dark"> element present.
//
// Mirroring slice 072's version-footer.spec.ts pattern: the
// unauthenticated path (/login) runs cleanly without seed data; the
// authed-path assertion stays commented pending the slice-069
// seed-data harness (same convention as the other e2e specs in this
// directory).
//
// Hard rule (slice-069 lesson, P0-A9): `expect` is imported AND used.

import { test, expect } from "@playwright/test";

test.describe("logo render", () => {
  test("/login shows the logo above the sign-in card with theme variants", async ({
    page,
  }) => {
    await page.goto("/login");

    // The logo on /login uses alt text "security-atlas" (the wordmark on
    // the login page is the brand name itself — accessible name lives on
    // the image alt). Locate via getByAltText for a stable selector.
    const logo = page.getByAltText("security-atlas").first();
    await expect(logo).toBeVisible();

    // The <picture> source set carries both theme variants. We assert
    // the parent <picture> exists and contains the dark + light <source>
    // elements with the right srcset values.
    const picture = logo.locator("xpath=ancestor::picture[1]");
    await expect(picture).toHaveCount(1);

    const darkSource = picture.locator(
      'source[media*="prefers-color-scheme: dark"]',
    );
    await expect(darkSource).toHaveAttribute("srcset", /logo-dark\.svg/);

    const lightSource = picture.locator(
      'source[media*="prefers-color-scheme: light"]',
    );
    await expect(lightSource).toHaveAttribute("srcset", /logo-light\.svg/);

    // The <img> fallback is the light variant (per the slice 057
    // <picture> pattern: fallback = light, sources override per-scheme).
    await expect(logo).toHaveAttribute("src", /logo-light\.svg/);
  });

  test("/login serves the favicon set declared via Metadata API", async ({
    page,
  }) => {
    await page.goto("/login");

    // Favicon: served from /favicon.ico via the Next.js Metadata API
    // `icons.icon` declaration in app/layout.tsx. The endpoint MUST
    // return 200 with image/x-icon (or image/vnd.microsoft.icon).
    const faviconResp = await page.request.get("/favicon.ico");
    expect(faviconResp.status()).toBe(200);
    const faviconType = faviconResp.headers()["content-type"] ?? "";
    expect(faviconType).toMatch(/image\/(x-icon|vnd\.microsoft\.icon)/);

    // PWA icons: 192 + 512 PNG.
    const icon192Resp = await page.request.get("/icon-192.png");
    expect(icon192Resp.status()).toBe(200);
    expect(icon192Resp.headers()["content-type"]).toContain("image/png");

    const icon512Resp = await page.request.get("/icon-512.png");
    expect(icon512Resp.status()).toBe(200);
    expect(icon512Resp.headers()["content-type"]).toContain("image/png");

    // Apple touch icon: 180x180 PNG.
    const appleResp = await page.request.get("/apple-touch-icon.png");
    expect(appleResp.status()).toBe(200);
    expect(appleResp.headers()["content-type"]).toContain("image/png");
  });

  test("/login serves the OG + Twitter cards as static public assets", async ({
    page,
  }) => {
    // The cards are declared via the Next.js Metadata API
    // (openGraph.images + twitter.images), pointing at static assets
    // under web/public/. Verify they're reachable; the unfurl behavior
    // itself is observable only when a scraper hits the deployed site.
    const ogResp = await page.request.get("/og-image.png");
    expect(ogResp.status()).toBe(200);
    expect(ogResp.headers()["content-type"]).toContain("image/png");

    const twitterResp = await page.request.get("/twitter-card.png");
    expect(twitterResp.status()).toBe(200);
    expect(twitterResp.headers()["content-type"]).toContain("image/png");
  });

  // The authed-layout assertion lives commented pending the slice-069
  // seed-data harness. When the harness lands, this test asserts the
  // same logo (with the same <picture> theme-source structure) renders
  // on /dashboard via the `authedPage` fixture — the logo there is
  // wrapped in <Link href="/dashboard"> per AC-5 of slice 075.
  //
  // test("/dashboard shows the logo wrapped in a dashboard link", async ({
  //   authedPage,
  // }) => {
  //   await authedPage.goto("/dashboard");
  //   const logoLink = authedPage.getByRole("link", {
  //     name: "security-atlas — go to dashboard",
  //   });
  //   await expect(logoLink).toBeVisible();
  //   await expect(logoLink).toHaveAttribute("href", "/dashboard");
  //
  //   const picture = logoLink.locator("picture");
  //   await expect(picture).toHaveCount(1);
  //   await expect(
  //     picture.locator('source[media*="prefers-color-scheme: dark"]'),
  //   ).toHaveAttribute("srcset", /logo-dark\.svg/);
  // });
});
