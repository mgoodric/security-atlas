// #2096 — control detail layout: the UCF graph's labels and the sticky
// tab strip.
//
//   - Graph: on a control whose control title, anchor name and
//     requirement names are long, every node label is shown in full
//     (no `.slice()` truncation) and sits inside its node's box, and no
//     edge label's box overlaps any node's box.
//   - Tab strip: scrolling the control page never leaves coverage-table
//     content visible above the stuck tab strip, and the strip's
//     background is opaque, so table rows never show through it.
//
// Self-contained: every BFF endpoint the page reads is mocked with a
// schema-conformant payload (the same shapes control-detail-tabs.spec.ts
// uses), so the spec needs only an authed session, not seed data.
//
// #2374 — every mock payload is checked against its BFF wire type with
// `satisfies`, so a change to a response shape fails typecheck here
// instead of leaving the spec green against a payload the page no longer
// gets.

import type { Page } from "@playwright/test";

import type {
  ControlCoverage,
  ControlEffectiveness,
  ControlHistoryResponse,
  ControlLinkedPoliciesResponse,
  ControlLinkedRisksResponse,
  ControlStateResponse,
  CoverageRequirement,
  EffectiveScopeResponse,
} from "@/lib/api/control-detail";
import type { EvidenceListResponse } from "@/lib/api/evidence";

import { expect, seeded, test } from "./fixtures";

const LONG_CONTROL_TITLE =
  "Background checks during hiring and periodic re-screening for personnel with privileged production access";
const LONG_ANCHOR_NAME =
  "Personnel screening, background verification and rescreening of individuals";
const OOS_FV = "11111111-1111-1111-1111-1111111100ff";

const REQUIREMENTS = Array.from(
  { length: 8 },
  (_, i): CoverageRequirement => ({
    edge_id: `00000000-0000-0000-0000-0000000001${String(i).padStart(2, "0")}`,
    requirement_id: `REQ-${i}`,
    code: `PS.SCREEN-${i}.a.${i}`,
    title: `Background checks during hiring, onboarding and role changes for personnel in category ${i} with access to customer data`,
    framework_slug: `fw-${i}`,
    framework_name:
      i % 2 === 0 ? "NIST SP 800-53 Rev. 5 Moderate Baseline" : "ISO/IEC 27001",
    framework_version: "2022",
    framework_version_id: i === 7 ? OOS_FV : seeded.frameworkVersionId,
    framework_version_status: "active",
    relationship_type: ["equal", "subset_of", "superset_of", "intersects_with"][
      i % 4
    ],
    strength: 0.5 + i / 20,
    coverage: 0.9,
    source_attribution: "scf",
  }),
);

async function mockControlEndpoints(page: Page): Promise<void> {
  const id = seeded.controlId;
  const json = (body: unknown) => ({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

  await page.route(`**/api/controls/${id}/coverage`, (route) =>
    route.fulfill(
      json({
        control: {
          id,
          bundle_id: "CTRL-PERSONNEL-SCREENING-0042",
          version: 1,
          title: LONG_CONTROL_TITLE,
          control_family: "Human Resources Security",
          owner_role: "People Operations",
          implementation_type: "manual",
          lifecycle_state: "active",
          freshness_class: "annual",
        },
        anchor: {
          id: "11111111-1111-1111-1111-111111110002",
          scf_id: "SCF:HRS-04",
          family: "HRS",
          name: LONG_ANCHOR_NAME,
          description: "Personnel screening anchor",
        },
        requirements: REQUIREMENTS,
      } satisfies ControlCoverage),
    ),
  );
  await page.route(`**/api/controls/${id}/state`, (route) =>
    route.fulfill(
      json({
        control_id: id,
        states: [],
        count: 0,
      } satisfies ControlStateResponse),
    ),
  );
  await page.route(`**/api/controls/${id}/effectiveness`, (route) => {
    const end = new Date();
    return route.fulfill(
      json({
        control_id: id,
        pass_rate: 1,
        pass_count: 4,
        total_count: 4,
        window_start: new Date(end.getTime() - 30 * 86_400_000).toISOString(),
        window_end: end.toISOString(),
      } satisfies ControlEffectiveness),
    );
  });
  await page.route(`**/api/controls/${id}/effective-scope?**`, (route) => {
    const fv =
      new URL(route.request().url()).searchParams.get("framework_version") ??
      "";
    const inScope = fv !== OOS_FV;
    return route.fulfill(
      json({
        control_id: id,
        framework_version_id: fv,
        framework_scope_id: "00000000-0000-0000-0000-0000000000f1",
        effective_scope: inScope
          ? [
              {
                id: "00000000-0000-0000-0000-0000000000c1",
                label: "prod-us",
                dimensions: {},
              },
            ]
          : [],
        effective_scope_count: inScope ? 1 : 0,
        in_scope: inScope,
        out_of_scope_reason: inScope ? undefined : "no matching scope cells",
      } satisfies EffectiveScopeResponse),
    );
  });
  await page.route("**/api/evidence?**", (route) =>
    route.fulfill(
      json({
        control_id: id,
        evidence: [],
        count: 0,
        total: 0,
        next_cursor: "",
      } satisfies EvidenceListResponse),
    ),
  );
  await page.route(`**/api/controls/${id}/policies`, (route) =>
    route.fulfill(
      json({
        control_id: id,
        policies: [],
        count: 0,
      } satisfies ControlLinkedPoliciesResponse),
    ),
  );
  await page.route(`**/api/controls/${id}/risks`, (route) =>
    route.fulfill(
      json({
        control_id: id,
        risks: [],
        count: 0,
      } satisfies ControlLinkedRisksResponse),
    ),
  );
  await page.route(`**/api/controls/${id}/history`, (route) =>
    route.fulfill(
      json({
        control_id: id,
        history: [],
        count: 0,
        next_cursor: "",
      } satisfies ControlHistoryResponse),
    ),
  );
}

async function gotoControl(page: Page, tab?: string): Promise<void> {
  const coverage = page.waitForResponse(
    (r) =>
      r.url().includes(`/api/controls/${seeded.controlId}/coverage`) &&
      r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto(`/controls/${seeded.controlId}${tab ? `?tab=${tab}` : ""}`);
  await coverage;
}

type Box = { x: number; y: number; w: number; h: number };

test.describe("control detail layout (#2096)", () => {
  test.beforeEach(async ({ authedPage: page }) => {
    await mockControlEndpoints(page);
  });

  test("graph: long labels wrap in full inside their nodes; edge labels clear every node", async ({
    authedPage: page,
  }) => {
    await gotoControl(page, "map");
    const viz = page.getByTestId("ucf-mini-viz");
    await expect(viz).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("ucf-viz-requirement")).toHaveCount(
      REQUIREMENTS.length,
    );

    const layout = await viz.evaluate((svg) => {
      const box = (el: Element) => {
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y, w: r.width, h: r.height };
      };
      const groups = Array.from(
        svg.querySelectorAll(
          '[data-testid="ucf-viz-control"], [data-testid="ucf-viz-anchor"], [data-testid="ucf-viz-requirement"]',
        ),
      );
      return {
        nodes: groups.map((g) => ({
          rect: box(g.querySelector('[data-testid="ucf-viz-node"]')!),
          texts: Array.from(
            g.querySelectorAll('[data-testid="ucf-viz-node-text"]'),
          ).map((t) => ({ box: box(t), text: t.textContent ?? "" })),
        })),
        edgeLabels: Array.from(
          svg.querySelectorAll('[data-testid="ucf-viz-edge-label"]'),
        ).map((t) => ({ box: box(t), text: t.textContent ?? "" })),
      };
    });

    // No truncation: each full label is present in its node's text.
    const nodeText = (i: number) =>
      layout.nodes[i].texts.map((t) => t.text).join(" ");
    expect(nodeText(0)).toContain(LONG_CONTROL_TITLE);
    expect(nodeText(1)).toContain(LONG_ANCHOR_NAME);
    REQUIREMENTS.forEach((req, i) => {
      expect(nodeText(i + 2)).toContain(req.title);
      expect(nodeText(i + 2)).toContain(`${req.framework_name} · ${req.code}`);
    });

    // Every text line sits inside its node's box.
    const inside = (inner: Box, outer: Box) =>
      inner.x >= outer.x - 0.5 &&
      inner.y >= outer.y - 0.5 &&
      inner.x + inner.w <= outer.x + outer.w + 0.5 &&
      inner.y + inner.h <= outer.y + outer.h + 0.5;
    for (const node of layout.nodes) {
      for (const t of node.texts) {
        expect(
          inside(t.box, node.rect),
          `"${t.text}" ${JSON.stringify(t.box)} escapes node ${JSON.stringify(
            node.rect,
          )}`,
        ).toBe(true);
      }
    }

    // No edge label overlaps any node.
    const overlaps = (a: Box, b: Box) =>
      a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
    expect(layout.edgeLabels.length).toBe(REQUIREMENTS.length + 1);
    for (const label of layout.edgeLabels) {
      for (const node of layout.nodes) {
        expect(
          overlaps(label.box, node.rect),
          `edge label "${label.text}" overlaps node ${JSON.stringify(
            node.rect,
          )}`,
        ).toBe(false);
      }
    }
  });

  test("tab strip: scrolling never shows coverage-table rows above or through the stuck strip", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 640 });
    await gotoControl(page);
    await expect(page.getByTestId("coverage-row")).toHaveCount(
      REQUIREMENTS.length,
      { timeout: 30_000 },
    );

    const tabs = page.getByTestId("control-tabs");
    const bg = await tabs.evaluate(
      (el) => getComputedStyle(el).backgroundColor,
    );
    // An opaque colour: no `transparent`, and no alpha below 1 in either
    // the legacy `rgba(r, g, b, a)` or the `fn(... / a)` syntax.
    expect(bg, `tab strip background ${bg}`).not.toMatch(
      /transparent|[,/]\s*(0|0?\.\d+)\)$/,
    );

    const main = page.locator("main#main-content");
    const maxScroll = await main.evaluate(
      (el) => el.scrollHeight - el.clientHeight,
    );
    expect(maxScroll).toBeGreaterThan(200);

    for (let top = 0; top <= maxScroll + 40; top += 40) {
      const frame = await main.evaluate((el, y) => {
        el.scrollTop = y;
        const m = el.getBoundingClientRect();
        const bar = el
          .querySelector('[data-testid="control-tabs"]')!
          .getBoundingClientRect();
        const rows = Array.from(
          el.querySelectorAll('[data-testid="coverage-row"]'),
        ).map((r) => {
          const b = r.getBoundingClientRect();
          return { top: b.top, bottom: b.bottom };
        });
        return {
          mainTop: m.top,
          barTop: bar.top,
          barBottom: bar.bottom,
          rows,
        };
      }, top);

      // The strip never sits below the top of the scroll container while
      // content above it is scrolled away: nothing can show through a gap.
      expect(frame.barTop).toBeGreaterThanOrEqual(frame.mainTop - 0.5);
      for (const row of frame.rows) {
        // The part of a row that is inside the scroll container's
        // viewport and above the strip's bottom edge must be under the
        // (opaque) strip, never in a band between the container top and
        // the strip.
        const visibleTop = Math.max(row.top, frame.mainTop);
        if (visibleTop < frame.barTop - 0.5) {
          expect(
            row.bottom,
            `row visible above the tab strip at scrollTop=${top}`,
          ).toBeLessThanOrEqual(frame.mainTop + 0.5);
        }
      }
    }
  });
});
