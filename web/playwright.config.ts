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
  // The specs are sequential against a single shared backend; parallelism
  // across files (workers) is fine, but inside-file tests run serially so
  // a sign-in in one test does not race with another's network mocks.
  fullyParallel: false,
  forbidOnly: isCI,
  retries: isCI ? 1 : 0,
  workers: isCI ? 2 : undefined,
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],

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
  // CI: rely on the docker-compose self-host bundle (the
  // `Frontend · Playwright e2e` job in .github/workflows/ci.yml brings up
  // postgres + nats + minio + atlas + web before invoking `playwright
  // test`). `reuseExistingServer: !isCI` keeps either path one-command.
  webServer: {
    command: "npm start",
    url: baseURL,
    reuseExistingServer: !isCI,
    timeout: 120_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
