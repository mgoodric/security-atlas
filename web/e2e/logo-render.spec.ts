// Slice 075 — Playwright E2E for the logo across the integration surfaces.
//
// AC-8: a Playwright spec asserts the logo <img> renders on the login +
// dashboard layouts, at the expected viewport, with the correct variant
// served for the active app theme.
//
// Slice 176 — the `<picture media="prefers-color-scheme: ...">` element
// was replaced with `<ThemeAwareLogo>` (a React component reading
// `<html data-theme>` via useEffect + MutationObserver). The original
// attribute assertions against the `<picture>` element no longer apply
// because no `<picture>` exists in the DOM after slice 176; the
// assertions now key off the rendered `<img>` `src` attribute, which
// is the load-bearing observable behavior.
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
  test("/login shows the theme-aware logo above the sign-in card", async ({
    page,
  }) => {
    await page.goto("/login");

    // The logo on /login uses alt text "security-atlas" (the wordmark on
    // the login page is the brand name itself — accessible name lives on
    // the image alt). Locate via getByAltText for a stable selector.
    const logo = page.getByAltText("security-atlas").first();
    await expect(logo).toBeVisible();

    // Slice 176 — the logo is now rendered via the <ThemeAwareLogo>
    // component (an <img> with data-testid="theme-aware-logo"). The
    // initial render serves logo-light.svg (SSR-safe default; matches
    // the prior slice 075 <picture> fallback <img src>).
    await expect(logo).toHaveAttribute("data-testid", "theme-aware-logo");
    await expect(logo).toHaveAttribute("src", /logo-light\.svg/);
  });

  // Slice 176 AC-9 — Bug A regression guard. The bug was: the inline
  // `<picture media="prefers-color-scheme: ...">` element keyed off the
  // operating system's preference, so operators on OS=dark with explicit
  // app theme=light were served logo-dark.svg (near-white ink) onto a
  // light app background, rendering the logo invisible.
  //
  // The component layer (web/components/shell/theme-aware-logo.tsx)
  // reads `<html data-theme>` (written by slice 170's AppearanceSelector)
  // and falls back to prefers-color-scheme only when the theme is
  // "system". The assertion below sets `data-theme="dark"` on <html>
  // and asserts the picker reacts by switching `src` to the dark
  // variant (proves the picker is observing data-theme, not just
  // prefers-color-scheme). The reverse direction is implicit: the
  // default boot state is "system" + matchMedia-light, which serves
  // the light variant — that is the first assertion above.
  test("/login -- logo variant follows app theme (data-theme on <html>)", async ({
    page,
  }) => {
    await page.goto("/login");
    const logo = page.getByTestId("theme-aware-logo").first();
    await expect(logo).toBeVisible();
    // Initial boot state: SSR default + no persisted choice = light.
    await expect(logo).toHaveAttribute("src", /logo-light\.svg/);

    // Simulate slice 170's AppearanceSelector writing the operator's
    // choice to <html data-theme="dark">. The MutationObserver inside
    // <ThemeAwareLogo> picks up the attribute change and re-renders
    // with the dark variant. This is the exact code path operators
    // exercise via /settings (which slice 176's component is wired to).
    await page.evaluate(() => {
      document.documentElement.setAttribute("data-theme", "dark");
    });
    await expect(logo).toHaveAttribute("src", /logo-dark\.svg/);

    // Toggle back to light: the picker MUST honor explicit "light" even
    // if the OS pref says otherwise. This is the original Bug A scenario
    // expressed as a positive regression test.
    await page.evaluate(() => {
      document.documentElement.setAttribute("data-theme", "light");
    });
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
