// Slice 178 — Playwright runner config for the UI honesty audit harness.
//
// This is a SEPARATE Playwright project from `web/e2e/` (P0-178-5).
// Intent split:
//   * `web/e2e/`        — functional contracts. Did the dashboard render
//                         six bound panels? Did the BFF wire to the
//                         right API? Pass/fail blocks merges (slice
//                         069 promotion).
//   * `web/e2e-audit/`  — UI honesty. Does the live UI show anything
//                         that isn't actually shipped (placeholder
//                         cards, "coming soon" buttons, dead anchors,
//                         flagged components)? Pass/fail is INFORMATIONAL
//                         only; findings file as spillover slices
//                         (slice 178 AC-14 / AC-17).
//
// Scope (slice 178):
//   * chromium ONLY (matches slice 069 — firefox + webkit deferred).
//   * Specs live in `web/e2e-audit/**/*.spec.ts`. v1 ships one spec
//     (`ui-honesty.spec.ts`) iterating all 10 audited routes.
//   * Read-only enforced structurally by `lib/make-read-only.ts`.
//   * webServer is `npm start` on :3000 when run locally; in CI the
//     `Frontend · UI honesty (advisory)` workflow brings up postgres +
//     nats + minio + atlas + web before invoking this config.

import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.PLATFORM_BASE_URL || "http://localhost:3000";
const isCI = !!process.env.CI;

// Reports root — gitignored under `reports/local-prod/` for the
// operator-local prod-run case (P0-178-3). Seeded-stack runs commit only
// the curated `docs/audit-log/178-ui-honesty-first-pass.md` summary, NOT
// the raw `reports/` JSON / screenshots (those are CI artifacts only).
const REPORTS_DIR = "reports";

export default defineConfig({
  testDir: ".",
  // Playwright picks up `*.spec.ts` only. The `*.test.ts` files in
  // `lib/` are vitest unit tests for the pure-logic modules — they
  // run under `npm run test` (see `web/vitest.config.ts`), NOT under
  // Playwright.
  testMatch: /.*\.spec\.ts$/,
  // Each route is a self-contained read-only navigation; serial within
  // a file keeps the screenshot ordering deterministic for diff review.
  fullyParallel: false,
  forbidOnly: isCI,
  retries: isCI ? 1 : 0,
  workers: 1,
  reporter: [
    ["list"],
    [
      "html",
      { open: "never", outputFolder: `${REPORTS_DIR}/playwright-report` },
    ],
    ["json", { outputFile: `${REPORTS_DIR}/playwright-results.json` }],
  ],

  outputDir: `${REPORTS_DIR}/playwright-output`,

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
  // CI: the workflow brings up the docker-compose stack and starts the
  // web server; `reuseExistingServer: isCI` keeps either path one
  // command.
  webServer: {
    command: "npm start",
    url: baseURL,
    reuseExistingServer: isCI,
    timeout: 120_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});
