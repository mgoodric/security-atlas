// security-atlas#2054 — the app renders in Geist, not the browser serif.
//
// `web/app/globals.css` once mapped `--font-sans: var(--font-sans)`. A
// self-referencing custom property is invalid, so every `font-sans`
// element fell back to Times while the Geist download (exposed by the
// root layout as `--font-geist-sans`) went unused. This guards the
// mapping at the computed-style level, where the regression is visible.
//
// /login renders without seed data, and its body and headings inherit
// from the same root layout as every authed page.

import { test, expect } from "@playwright/test";

// next/font names the family "Geist" (plus a "Geist Fallback" metric
// face). Geist Mono must not satisfy this.
const GEIST_SANS = /^["']?Geist["']?(,|$)/;

test.describe("font family", () => {
  test("/login body and headings compute to Geist", async ({ page }) => {
    await page.goto("/login");

    const bodyFont = await page.evaluate(
      () => getComputedStyle(document.body).fontFamily,
    );
    expect(bodyFont).toMatch(GEIST_SANS);

    const headingFonts = await page.evaluate(() =>
      Array.from(document.querySelectorAll("h1, h2, h3")).map(
        (h) => getComputedStyle(h).fontFamily,
      ),
    );
    for (const f of headingFonts) {
      expect(f).toMatch(GEIST_SANS);
    }
  });
});
