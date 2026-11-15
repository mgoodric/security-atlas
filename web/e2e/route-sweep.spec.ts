// #2080 — route sweep: every authenticated route, visited as an admin,
// in the app shell.
//
// The route list is DERIVED from the filesystem, not hand-written: every
// directory under the authenticated trees of `web/app` that holds a
// `page.tsx` is a route, so a page added later is swept without anyone
// remembering to add it here. Dynamic segments resolve through
// `PARAM_VALUES`; a segment with no entry fails the sweep and names
// itself, rather than being silently skipped.
//
// ENFORCED at 1316px and 1200px (the two widths the UI audit measured):
//
//   * a non-generic document title, "<Page> · security-atlas";
//   * no horizontal scroll on the page or on the main column (a wide
//     table must scroll inside its own container);
//   * the top bar stays on one line;
//   * a visible focus ring, drawn when the first tabbable control (the
//     skip link) and the first tabbable control inside the main column
//     take keyboard focus.
//
// REPORTED, never failed: axe serious + critical violations. Each route
// writes `axe-report/<route>.json` (uploaded by CI as the
// `route-sweep-axe-report` artifact) and attaches the same JSON to the
// test. Enforcement is a later ticket.
//
// Waits are web-first (`toBeVisible`, `toHaveTitle`, `expect.poll`) plus
// `networkidle` so client pages have rendered their data before the
// width is measured. No fixed sleeps.

import fs from "node:fs";
import path from "node:path";

import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";

import { expect, seeded, test } from "./fixtures";

const APP_DIR = path.join(__dirname, "..", "app");

// The authenticated trees. `(authed)` is the route group; `/admin`,
// `/audit` and `/audit-log` live outside it because their paths are
// literal, but render the same shell.
const AUTHED_ROOTS = ["(authed)", "admin", "audit", "audit-log"];

// A well-formed id that matches no row: detail routes without a seeded
// row render their not-found or error state, which must still sit in the
// shell and pass every check.
const NO_ROW_ID = "00000000-0000-0000-0000-000000002080";

// Dynamic segment -> value, keyed by the route pattern up to and
// including the segment. Seeded rows where the harness has one.
const PARAM_VALUES: Record<string, string> = {
  "/action-plans/[id]": NO_ROW_ID,
  "/audits/[id]": seeded.auditPeriodId,
  "/board-packs/[id]": NO_ROW_ID,
  "/catalog/scf/[id]": NO_ROW_ID,
  "/controls/[id]": seeded.controlId,
  "/csf/[framework_version_id]": seeded.frameworkVersionId,
  "/dashboards/metrics/[id]": NO_ROW_ID,
  "/evidence/[id]": NO_ROW_ID,
  "/exceptions/[id]": NO_ROW_ID,
  "/exceptions/[id]/[action]": "revoke",
  "/framework-scopes/[framework_version_id]": seeded.frameworkVersionId,
  "/oscal/component-definitions/[id]": NO_ROW_ID,
  "/personnel-security/[id]": NO_ROW_ID,
  "/policies/[id]": NO_ROW_ID,
  "/questionnaires/[id]": NO_ROW_ID,
  "/requirements/[id]": NO_ROW_ID,
  "/risks/[id]": NO_ROW_ID,
  "/vendors/[id]": NO_ROW_ID,
  "/audit/[controlId]": seeded.controlId,
};

// An unmatched URL: the root not-found, which must render in the shell
// for a signed-in user.
const NOT_FOUND_ROUTE = "/route-sweep-no-such-page";

const WIDTHS = [1316, 1200] as const;

type Route = { pattern: string; url: string };

function discoverRoutes(): Route[] {
  const patterns: string[] = [];
  const walk = (dir: string, segments: string[]) => {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    if (entries.some((e) => e.isFile() && e.name === "page.tsx")) {
      patterns.push("/" + segments.join("/"));
    }
    for (const e of entries) {
      if (!e.isDirectory() || e.name.startsWith("_")) continue;
      // Route groups `(x)` add no path segment.
      const seg = /^\(.*\)$/.test(e.name) ? [] : [e.name];
      walk(path.join(dir, e.name), [...segments, ...seg]);
    }
  };
  for (const root of AUTHED_ROOTS) {
    const seg = /^\(.*\)$/.test(root) ? [] : [root];
    walk(path.join(APP_DIR, root), seg);
  }
  return patterns.sort().map((pattern) => ({ pattern, url: resolve(pattern) }));
}

function resolve(pattern: string): string {
  const parts = pattern.split("/");
  return parts
    .map((part, i) => {
      if (!part.startsWith("[")) return part;
      const key = parts.slice(0, i + 1).join("/");
      const value = PARAM_VALUES[key];
      if (value === undefined) {
        throw new Error(
          `route sweep: no value for dynamic segment ${key}; add it to PARAM_VALUES in e2e/route-sweep.spec.ts`,
        );
      }
      return encodeURIComponent(value);
    })
    .join("/");
}

const ROUTES: Route[] = [
  ...discoverRoutes(),
  { pattern: NOT_FOUND_ROUTE, url: NOT_FOUND_ROUTE },
];

// Overflow of more than a pixel counts; sub-pixel rounding does not.
const TOLERANCE_PX = 1;

async function horizontalOverflow(page: Page) {
  return page.evaluate(() => {
    const doc = document.documentElement;
    const main = document.getElementById("main-content");
    const bar = document.querySelector<HTMLElement>(
      '[data-testid="app-topbar"]',
    );
    return {
      page: doc.scrollWidth - doc.clientWidth,
      main: main ? main.scrollWidth - main.clientWidth : 0,
      topbar: bar ? bar.scrollWidth - bar.clientWidth : 0,
    };
  });
}

// True when the focused element draws a ring: an outline or a box-shadow
// (Tailwind's `ring-*` utilities are box-shadows).
async function activeHasFocusRing(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || el === document.body) return false;
    const s = getComputedStyle(el);
    const outline =
      s.outlineStyle !== "none" && parseFloat(s.outlineWidth || "0") > 0;
    const shadow = s.boxShadow !== "" && s.boxShadow !== "none";
    return outline || shadow;
  });
}

async function describeActive(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el) return "(none)";
    const id = el.getAttribute("data-testid") ?? el.id ?? "";
    const text = (el.textContent ?? "").trim().slice(0, 40);
    return `<${el.tagName.toLowerCase()}${id ? ` #${id}` : ""}> ${text}`;
  });
}

async function checkFocusRings(page: Page, label: string) {
  // The first tabbable control on the page: the skip link. The caller
  // has just loaded the page, so Tab starts from the document.
  await page.keyboard.press("Tab");
  await expect
    .poll(() => activeHasFocusRing(page), {
      message: `${label}: first tabbable control (${await describeActive(
        page,
      )}) shows no focus ring`,
    })
    .toBe(true);

  // The first tabbable control inside the main column. `#main-content`
  // has tabindex=-1, so focusing it and pressing Tab lands on its first
  // tabbable descendant. A page with nothing tabbable in its body (a
  // pure read-only panel) has nothing to check here.
  await page.locator("#main-content").focus();
  await page.keyboard.press("Tab");
  const insideMain = await page.evaluate(() => {
    const main = document.getElementById("main-content");
    return !!main && main.contains(document.activeElement);
  });
  if (insideMain) {
    await expect
      .poll(() => activeHasFocusRing(page), {
        message: `${label}: first control in the main column (${await describeActive(
          page,
        )}) shows no focus ring`,
      })
      .toBe(true);
  }
}

function reportSlug(pattern: string): string {
  return (
    pattern
      .replace(/^\//, "")
      .replace(/[[\]]/g, "")
      .replace(/[^a-zA-Z0-9_-]+/g, "_") || "root"
  );
}

const AXE_REPORT_DIR = path.join(__dirname, "..", "axe-report");

test.describe("route sweep (#2080)", () => {
  test("every authenticated tree is discovered", () => {
    const patterns = ROUTES.map((r) => r.pattern);
    for (const expected of [
      "/dashboard",
      "/risks",
      "/settings",
      "/admin",
      "/audit",
      "/audit-log",
    ]) {
      expect(patterns).toContain(expected);
    }
  });

  for (const route of ROUTES) {
    test(`${route.pattern}`, async ({ authedPage: page }, testInfo) => {
      for (const width of WIDTHS) {
        const label = `${route.pattern} @ ${width}px`;
        // A fresh load per width: the page lays out at that width from
        // the start, and keyboard focus starts from the document.
        await page.setViewportSize({ width, height: 900 });
        await page.goto(route.url);
        await expect(page.locator("main#main-content")).toBeVisible();
        await expect(page.getByTestId("sidebar-desktop")).toBeVisible();
        await page.waitForLoadState("networkidle");

        // Title: "<Page> · security-atlas", never the bare default.
        await expect(page).toHaveTitle(/^\S.* · security-atlas$/);

        // Poll so the layout has settled after the resize; then assert
        // each surface on its own so a failure names the one that
        // overflowed.
        await expect
          .poll(
            async () => {
              const o = await horizontalOverflow(page);
              return Math.max(o.page, o.main, o.topbar);
            },
            { message: `${label}: horizontal overflow` },
          )
          .toBeLessThanOrEqual(TOLERANCE_PX)
          .catch(() => undefined);
        const overflow = await horizontalOverflow(page);
        expect(
          overflow.page,
          `${label}: page scrolls sideways`,
        ).toBeLessThanOrEqual(TOLERANCE_PX);
        expect(
          overflow.main,
          `${label}: main column scrolls sideways`,
        ).toBeLessThanOrEqual(TOLERANCE_PX);
        expect(
          overflow.topbar,
          `${label}: top bar overflows its line`,
        ).toBeLessThanOrEqual(TOLERANCE_PX);
        await checkFocusRings(page, label);
      }

      // axe: report serious + critical, never fail (#2080; enforcement is
      // a later ticket). Runs against the last width's load.
      const results = await new AxeBuilder({ page }).analyze();
      const violations = results.violations
        .filter((v) => v.impact === "serious" || v.impact === "critical")
        .map((v) => ({
          id: v.id,
          impact: v.impact,
          help: v.help,
          helpUrl: v.helpUrl,
          nodes: v.nodes.map((n) => n.target.join(" ")),
        }));
      const report = { route: route.pattern, url: route.url, violations };
      fs.mkdirSync(AXE_REPORT_DIR, { recursive: true });
      fs.writeFileSync(
        path.join(AXE_REPORT_DIR, `${reportSlug(route.pattern)}.json`),
        JSON.stringify(report, null, 2),
      );
      await testInfo.attach("axe-serious-critical", {
        body: JSON.stringify(report, null, 2),
        contentType: "application/json",
      });
      if (violations.length > 0) {
        testInfo.annotations.push({
          type: "axe",
          description: `${violations.length} serious/critical: ${violations
            .map((v) => v.id)
            .join(", ")}`,
        });
      }
    });
  }
});
