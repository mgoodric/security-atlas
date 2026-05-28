// Slice 069 — Playwright runner config for the security-atlas web app.
//
// Scope (slice 069):
//   * chromium ONLY (firefox + webkit deferred — see slice 069 P0-A1)
//   * Specs live in `web/e2e/**/*.spec.ts` (5 specs authored ahead of
//     this slice; see each spec's preamble comment)
//   * webServer is `npm start` on :3000 when run locally; in CI a real
//     docker-compose self-host bundle owns bring-up, so we
//     `reuseExistingServer` when CI=true
//   * Auth + seed data are established by the harness in `e2e/fixtures.ts`

import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.PLATFORM_BASE_URL || "http://localhost:3000";
const isCI = !!process.env.CI;

export default defineConfig({
  testDir: "./e2e",
  // Slice 201: global-setup mints a JWT via the env-gated atlas
  // endpoint `POST /v1/test/issue-jwt` and writes it into
  // `process.env.TEST_BEARER`. Replaces the static
  // `TEST_BEARER = "test-bearer-e2e"` literal that slice 197 broke by
  // retiring the slice 034 bearer middleware. The atlas server MUST be
  // started with ATLAS_TEST_MODE=1 (and ATLAS_ISSUER_URL set so the
  // OAuth keystore is wired) before this runs.
  globalSetup: require.resolve("./e2e/global-setup"),
  // Slice 348 P-3 — experiment: lift fullyParallel.
  //
  // Pre-slice-201 the `TEST_BEARER` was a static literal shared across
  // all specs, so two tests racing on the sign-in fixture could
  // corrupt each other's network-mock setup. Slice 201's JWT migration
  // moved bearer minting into `globalSetup` per worker, and per-test
  // `page.route` mocks are scoped to the page's BrowserContext (not
  // shared across tests). The static-bearer race that justified
  // `fullyParallel: false` no longer exists.
  //
  // Per slice 348 P-3 we enable `fullyParallel: true` and observe 3
  // consecutive CI runs on this branch. If all 3 are green, the flag
  // ships. If any run surfaces a race, the flag reverts and the
  // failure mode lands in the slice 348 decisions log (D-D1) for the
  // next polish round to address. P0-348-6 forbids keeping
  // `fullyParallel: true` if any CI run flakes during the 3-run
  // stress test.
  fullyParallel: true,
  forbidOnly: isCI,
  retries: isCI ? 1 : 0,
  workers: isCI ? 2 : undefined,
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],

  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],

  // Local dev: spin up `npm start` if nothing is listening on :3000.
  // CI: rely on the workflow's "Start web server" step (the
  // `Frontend · Playwright e2e` job in .github/workflows/ci.yml brings up
  // postgres + nats + minio + atlas + web before invoking `playwright
  // test`). `reuseExistingServer: isCI` keeps either path one-command:
  // attach to the workflow-spawned server in CI, spawn a fresh one
  // locally. Slice 119 — the prior `!isCI` was a polarity inversion: it
  // told Playwright to spawn its own server in CI, racing the workflow
  // step for :3000 and failing every run with "port 3000 already in use".
  webServer: {
    command: "npm start",
    url: baseURL,
    reuseExistingServer: isCI,
    timeout: 120_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
