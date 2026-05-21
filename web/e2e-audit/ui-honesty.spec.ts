// Slice 178 — UI honesty audit spec.
//
// One Playwright spec, one test case per audited route, iterating the
// manifest. Each test:
//
//   1. Navigates the authenticated read-only page to the route.
//   2. Waits for `networkidle` (slower than DOM ready; ensures every
//      panel's bound query resolved).
//   3. Captures the live fingerprint (testids + dead anchors + coming-
//      soon buttons + unset feature flags) via the `heuristics` module.
//   4. Captures a single full-page screenshot to
//      `reports/screenshots/<timestamp>/<route>.png`.
//   5. Diffs against the manifest entry. Findings accumulate in a
//      module-scoped collector; the final test in the spec writes the
//      consolidated JSON + Markdown report.
//
// The spec is INFORMATIONAL: it never asserts pass/fail on the count
// of findings. A failure here means the harness itself broke (network,
// auth, page render error), not that the UI is dishonest. That signal
// is delivered through the report + the CI sticky PR comment.

import { mkdirSync } from "node:fs";
import { resolve } from "node:path";

import { test as base, expect, resolveRoute } from "./fixtures";
import { captureFingerprint } from "./lib/heuristics";
import { diffRoute, sortFindings, type Finding } from "./lib/mockup-diff";
import { loadManifest } from "./lib/manifest";
import {
  buildContext,
  writeJSONReport,
  writeMarkdownReport,
} from "./lib/report";

const RUN_TIMESTAMP = new Date().toISOString().replace(/[:.]/g, "-");

const screenshotDir = resolve(
  __dirname,
  "reports",
  "screenshots",
  RUN_TIMESTAMP,
);
mkdirSync(screenshotDir, { recursive: true });

const COLLECTED: Finding[] = [];

const manifest = loadManifest();

base.describe("UI honesty audit (slice 178)", () => {
  for (const entry of manifest) {
    base(`audit ${entry.route}`, async ({ authedReadOnlyPage }) => {
      const page = authedReadOnlyPage;
      const url = resolveRoute(entry.route);
      // Slice 178 — networkidle is the slower wait that matches the
      // dashboard's six-panel TanStack Query bound state. domcontent
      // would fire before the panels render.
      await page.goto(url, { waitUntil: "networkidle" });

      // Screenshot first so a render-time crash still gives us the
      // partial paint.
      await page.screenshot({
        path: resolve(
          screenshotDir,
          `${entry.route.replace(/[\/:]/g, "_")}.png`,
        ),
        fullPage: true,
      });

      const live = await captureFingerprint(page, entry.route);
      const findings = diffRoute(live, entry);
      COLLECTED.push(...findings);

      // Soft assertion — log findings for the runner output, but
      // never fail the test. The report is the deliverable.
      if (findings.length > 0) {
        console.log(
          `[ui-honesty] ${entry.route}: ${findings.length} finding(s)`,
        );
      }
      // Hard expect: at least the page loaded (any testid). This
      // catches harness-broke vs UI-honesty cases. If the page
      // renders zero testids the audit can't say anything meaningful.
      if (entry.expectedTestIds.length > 0) {
        expect(
          live.testIds.length,
          `route ${entry.route} rendered zero testids — likely an auth or 5xx, not a UI-honesty signal`,
        ).toBeGreaterThan(0);
      }
    });
  }

  base.afterAll(async () => {
    // Slice 178 — the final report. Reports live under `reports/` which
    // is gitignored; the committed first-pass summary lives in
    // `docs/audit-log/178-ui-honesty-first-pass.md` (AC-16).
    const sorted = sortFindings(COLLECTED);
    const baseURL = process.env.PLATFORM_BASE_URL || "http://localhost:3000";
    const ctx = buildContext(baseURL);

    const reportPath = resolve(
      __dirname,
      "reports",
      `ui-honesty-${RUN_TIMESTAMP}`,
    );
    writeJSONReport(`${reportPath}.json`, ctx, sorted);
    writeMarkdownReport(`${reportPath}.md`, ctx, sorted);

    console.log(
      `[ui-honesty] wrote ${sorted.length} findings to ${reportPath}.{json,md}`,
    );
  });
});
