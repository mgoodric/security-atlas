// Slice 153 — Playwright E2E asserting the logo SVG + OG/Twitter card
// assets render under the Next.js production-build standalone server
// (not just `next dev`).
//
// Why this spec exists: slice 123 added `PUBLIC_STATIC_FILES` to
// web/proxy.ts to exempt 7 unauthenticated-referenced assets from the
// auth-redirect. That fix was verified in dev mode (logo-render.spec.ts
// passes against `next dev`). On v1.10.0 the operator reported the
// logo STILL doesn't render in the production-build standalone
// deployment. Root cause (slice-153 decisions D1): the Next.js
// `output: "standalone"` tracer does NOT copy `web/public/` into the
// standalone tree — server-traced output omits static assets by
// design, and `.next/static` is copied separately by the runtime
// stage of web.Dockerfile, but the comment in that Dockerfile
// asserted "There is no web/public directory" which became stale
// when slice 123 introduced web/public/. As a result `/logo-light.svg`
// returned 404 in the deployed image even though proxy.ts correctly
// let the request through.
//
// The fix lives in (a) deploy/docker/web.Dockerfile (added `COPY
// /app/web/public ./public` at the standalone root) and (b) the
// `build:standalone` script in web/package.json which does the same
// copy for local `node .next/standalone/web/server.js` invocations.
//
// Quarantine note (slice 082): this spec requires the production-build
// standalone server running. CI baseURL points at `next dev`, so the
// spec is guarded by ATLAS_PROD_BUILD env var (same pattern as
// bff-cookie-production-build.spec.ts from slice 146). Local invocation:
//
//   cd web
//   npm run build:standalone
//   PORT=3000 node .next/standalone/web/server.js &
//   ATLAS_PROD_BUILD=1 npx playwright test \
//     logo-render-production-build.spec.ts
//
// Once the slice-082 seed harness provisions a standalone server in
// the CI matrix, drop the ATLAS_PROD_BUILD guard.
//
// Hard rule (P0-A9 from slice 069 + P0-LOGO-4 from slice 153): no
// vendor-prefixed token strings anywhere in this file.

import { test, expect } from "@playwright/test";

const RUN_AGAINST_PROD_BUILD = !!process.env.ATLAS_PROD_BUILD;

test.describe("logo + public assets render in production-build standalone", () => {
  // Slice 351 (AC-4, disposition (b) — re-quarantine with justification
  // + spillover). Same real harness gap as
  // bff-cookie-production-build.spec.ts: this asserts the slice-153
  // standalone-public-assets regression that ONLY manifests under the
  // production-build standalone server (the `output: "standalone"`
  // tracer does not copy web/public/). CI runs against `npm start`, and
  // no CI job provisions the standalone server. Re-quarantined rather
  // than green-washed against the dev server. Shared spillover: slice
  // 387 (one standalone-server CI harness unblocks both prod-build
  // specs). Runnable locally per the invocation block above.
  test.skip(
    !RUN_AGAINST_PROD_BUILD,
    "ATLAS_PROD_BUILD not set — quarantined behind slice 387 (no CI harness for the production-build standalone server yet); runnable locally with ATLAS_PROD_BUILD=1",
  );

  test("/logo-light.svg returns 200 + image/svg+xml", async ({ page }) => {
    const resp = await page.request.get("/logo-light.svg");
    expect(resp.status()).toBe(200);
    expect(resp.headers()["content-type"] ?? "").toContain("image/svg+xml");
  });

  test("/logo-dark.svg returns 200 + image/svg+xml", async ({ page }) => {
    const resp = await page.request.get("/logo-dark.svg");
    expect(resp.status()).toBe(200);
    expect(resp.headers()["content-type"] ?? "").toContain("image/svg+xml");
  });

  test("/og-image.png returns 200 + image/png", async ({ page }) => {
    // Regression guard: the same standalone-public-assets gap that
    // hid the logo also hid the OG unfurl card. Slice 123 fixed the
    // proxy redirect; slice 153 fixes the standalone copy.
    const resp = await page.request.get("/og-image.png");
    expect(resp.status()).toBe(200);
    expect(resp.headers()["content-type"] ?? "").toContain("image/png");
  });

  test("/twitter-card.png returns 200 + image/png", async ({ page }) => {
    const resp = await page.request.get("/twitter-card.png");
    expect(resp.status()).toBe(200);
    expect(resp.headers()["content-type"] ?? "").toContain("image/png");
  });

  test("/favicon.ico returns 200 + favicon mime", async ({ page }) => {
    // Favicon under Next.js Metadata API lives under /app/favicon.ico
    // and is served from .next/static (not web/public). Asserted here
    // so a future static-asset reorganization doesn't silently break
    // the deployed favicon.
    const resp = await page.request.get("/favicon.ico");
    expect(resp.status()).toBe(200);
    expect(resp.headers()["content-type"] ?? "").toMatch(
      /image\/(x-icon|vnd\.microsoft\.icon)/,
    );
  });

  test("/login HTML references /logo-light.svg AND the asset resolves", async ({
    page,
  }) => {
    // End-to-end binding: the login page HTML and the asset path
    // both have to agree, and the asset has to be served. This is
    // the closest spec to the operator-reported failure ("the logo
    // is still not showing on the login screen").
    const loginResp = await page.request.get("/login");
    expect(loginResp.status()).toBe(200);
    const html = await loginResp.text();
    expect(html).toContain("/logo-light.svg");

    const assetResp = await page.request.get("/logo-light.svg");
    expect(assetResp.status()).toBe(200);
    expect(assetResp.headers()["content-type"] ?? "").toContain(
      "image/svg+xml",
    );
  });
});
