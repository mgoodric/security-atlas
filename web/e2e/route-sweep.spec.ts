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
//   * no serious or critical axe violation (#2097; the gate lives in
//     `axe-gate.ts` and is self-tested by `axe-gate.spec.ts`). Each
//     route also writes `axe-report/<route>.json` (uploaded by CI as the
//     `route-sweep-axe-report` artifact) and attaches the same JSON to
//     the test, pass or fail.
//
// ENFORCED once, statically: every route has a help module, or an
// exemption or a pending ticket in `lib/help/coverage.ts` (#2156).
//
// Waits are web-first (`toBeVisible`, `toHaveTitle`, `expect.poll`) plus
// `networkidle` so client pages have rendered their data before the
// width is measured. No fixed sleeps.

import fs from "node:fs";
import path from "node:path";

import type { Page } from "@playwright/test";

import {
  discoverAuthedRoutes,
  helpCoverageProblems,
  readHelpModules,
} from "../lib/help/coverage";
import { expectNoSeriousAxe, seriousAxeViolations } from "./axe-gate";
import { expect, seeded, test } from "./fixtures";

const APP_DIR = path.join(__dirname, "..", "app");
const HELP_DIR = path.join(__dirname, "..", "help", "modules");
// A tree holding one route a module covers and one deliberate route
// with no help, for the help check's self-test.
const FIXTURE_APP_DIR = path.join(
  __dirname,
  "..",
  "lib",
  "help",
  "__fixtures__",
  "app",
);

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
  "/controls/imports/[id]": NO_ROW_ID,
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
  "/risks/[id]/acceptance/[action]": "propose",
  "/risks/imports/[id]": NO_ROW_ID,
  "/vendors/[id]": NO_ROW_ID,
  "/audit/[controlId]": seeded.controlId,
};

// An unmatched URL: the root not-found, which must render in the shell
// for a signed-in user.
const NOT_FOUND_ROUTE = "/route-sweep-no-such-page";

const WIDTHS = [1316, 1200] as const;

type Route = { pattern: string; url: string };

// The walk lives in `lib/help/coverage.ts`, so the help-coverage check
// below sees exactly the routes this sweep visits.
function discoverRoutes(): Route[] {
  return discoverAuthedRoutes(APP_DIR).map((pattern) => ({
    pattern,
    url: resolve(pattern),
  }));
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

  // #2156 — every route has help: a module covers it, or
  // `lib/help/coverage.ts` lists it as exempt (with its reason) or
  // pending (with its open ticket). A stale list entry fails too.
  test("every route has a help module (#2156)", () => {
    const routes = ROUTES.map((r) => r.pattern).filter(
      (p) => p !== NOT_FOUND_ROUTE,
    );
    expect(helpCoverageProblems(routes, readHelpModules(HELP_DIR))).toEqual([]);
  });

  // The same check fails on a deliberate fixture route with no help.
  test("the help check fails on a route with no help (#2156)", () => {
    const routes = discoverAuthedRoutes(FIXTURE_APP_DIR, ["(authed)"]);
    expect(
      helpCoverageProblems(routes, readHelpModules(HELP_DIR), [], []),
    ).toEqual([
      "/no-help-fixture: no help module covers this route; add it to the routes of a file in web/help/modules/",
    ]);
  });

  // /audit-log is exempt from help because it only redirects. If it ever
  // renders a page of its own, this fails and the exemption goes.
  test("/audit-log still redirects to /activity (#2156)", async ({
    authedPage: page,
  }) => {
    await page.goto("/audit-log");
    await expect(page).toHaveURL(/\/activity(\?|$)/);
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

      // axe: serious + critical fail the route (#2097). Runs against the
      // last width's load. The report is written before the assertion,
      // so a failing route still leaves its JSON in the CI artifact.
      const violations = await seriousAxeViolations(page);
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
      expectNoSeriousAxe(violations, route.pattern);
    });
  }
});
