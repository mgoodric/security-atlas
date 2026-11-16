// #2057 — board-pack pages are readable in both app themes.
//
// The board-pack list uses the app's theme tokens; the pack sheet always
// renders as paper (the `.paper` scope in globals.css pins the light token
// set), whatever the app theme. This spec walks every visible text element
// on the list page and on the sheet, in dark and in light mode, and checks
// each one against WCAG 2.x AA contrast (4.5:1, or 3:1 for large text).
//
// Hermetic mock pattern (slice 594 / slice 662): the BFF responses for the
// pack list, the pack, and the /api/admin/me approver probe are fulfilled
// by route mocks, so the assertions do not depend on seeded data. The
// `authedPage` fixture only supplies the session cookie.
//
// The theme is forced the way an operator sets it: the /settings picker's
// localStorage key, which the layout's early-paint script turns into
// `class="dark"` on <html> before first paint.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const THEME_STORAGE_KEY = "security-atlas.settings.theme";
const DRAFT_ID = "00000000-0000-0000-0000-0000020570aa";
const PUBLISHED_ID = "00000000-0000-0000-0000-0000020570bb";

const SECTION_LABELS: Record<string, string> = {
  posture: "Posture summary",
  top_risks: "Top risks aging",
  coverage_trend: "Coverage trend",
  open_findings: "Open findings",
  vendor_burndown: "Vendor risk burndown",
  operational_metrics: "Operational metrics",
  investment: "Investment vs coverage",
  asks: "Asks of the board",
};

// Data chosen so every tone renders: each treatment chip, each freshness
// chip, positive/negative posture deltas, a "good" operational target and
// a past-due vendor warning.
const SECTION_DATA: Record<string, Record<string, unknown>> = {
  posture: {
    frameworks: [
      {
        slug: "soc2",
        name: "SOC 2",
        coverage_pct: 94,
        freshness_pct: 88,
        trend_arrow: "↑",
        delta: 3,
        state: "audit-ready",
      },
      {
        slug: "iso27001",
        name: "ISO 27001",
        coverage_pct: 71,
        freshness_pct: 64,
        trend_arrow: "↓",
        delta: -2,
        state: "regressed",
      },
      {
        slug: "hipaa",
        name: "HIPAA",
        coverage_pct: 55,
        freshness_pct: 70,
        trend_arrow: "→",
        delta: 0,
        state: "in-progress",
      },
    ],
  },
  top_risks: {
    top_risks: [
      "untreated",
      "mitigate",
      "accept (proposed)",
      "transfer",
      "avoid",
    ].map((treatment, i) => ({
      id: `RISK-00${i + 1}`,
      title: `Risk ${i + 1}`,
      category: "access",
      treatment,
      residual_severity: 3.5 - i * 0.5,
      age_days: 30 + i,
    })),
  },
  coverage_trend: {
    baseline_coverage_pct: 80,
    coverage_pct: 84,
    coverage_delta: 4,
  },
  open_findings: {
    findings_count: 4,
    findings: ["fresh", "stale", "expired", "missing"].map((status, i) => ({
      evaluation_id: `eval-${i}`,
      control_id: `IAC-0${i + 1}`,
      scope_cell_id: "cell-1",
      evaluated_at: "2026-03-30T12:00:00Z",
      freshness_status: status,
    })),
  },
  vendor_burndown: {
    vendor_burndown_total: 8,
    vendor_burndown_on_time: 6,
    vendor_burndown_past_due: 2,
    vendor_burndown_on_time_pct: 75,
  },
  operational_metrics: {
    phishing_pass_rate_pct: 97,
    p1_patch_median_days: 9,
    incident_count: 1,
    vendor_reviews_on_time: 6,
    vendor_reviews_total: 8,
  },
  investment: {
    spend_usd: 120000,
    coverage_delta: 4,
    cost_per_coverage_point: 30000,
  },
  asks: {},
};

function buildPack(id: string, status: "draft" | "published") {
  const published = status === "published";
  const sections: Record<string, unknown> = {};
  Object.keys(SECTION_LABELS).forEach((key, i) => {
    // The draft omits the asks section (the "not generated" state) and
    // leaves every other section alternately approved / not approved.
    if (!published && key === "asks") return;
    sections[key] = {
      key,
      title: SECTION_LABELS[key],
      templated_text: `Templated narrative for ${SECTION_LABELS[key]}.`,
      override_text: "",
      approved: published || i % 2 === 0,
      data: SECTION_DATA[key],
    };
  });
  return {
    id,
    period_end: "2026-03-31",
    status,
    narrative_md: "Quarterly board pack — e2e mock.",
    published_by: published ? "Sam Rivera (CISO)" : "",
    published_at: published ? "2026-04-02T09:00:00Z" : undefined,
    created_at: "2026-03-31T00:00:00Z",
    updated_at: "2026-03-31T00:00:00Z",
    content: {
      period_end: "2026-03-31",
      generated_at: "2026-03-31T00:00:00Z",
      status,
      sections,
    },
  };
}

async function mockBoardPacks(page: Page): Promise<void> {
  const draft = buildPack(DRAFT_ID, "draft");
  const published = buildPack(PUBLISHED_ID, "published");
  await page.route("**/api/admin/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ is_admin: true }),
    }),
  );
  await page.route("**/api/board-packs", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ packs: [published, draft] }),
    }),
  );
  for (const pack of [draft, published]) {
    await page.route(`**/api/board-packs/${pack.id}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(pack),
      }),
    );
  }
}

async function useTheme(page: Page, theme: "dark" | "light"): Promise<void> {
  await page.emulateMedia({ colorScheme: theme });
  await page.addInitScript(
    ([key, value]) => window.localStorage.setItem(key, value),
    [THEME_STORAGE_KEY, theme] as const,
  );
}

type ContrastFailure = {
  text: string;
  ratio: number;
  required: number;
  fg: string;
  bg: string;
};

// contrastFailures runs in the page: for every visible element under `root`
// that owns a non-blank text node, it composites the element's text colour
// over its effective background (the stack of ancestor backgrounds, alpha
// included) and returns the ones below WCAG AA. Colours are resolved through
// a canvas, so oklch / oklab / color-mix values all reduce to sRGB.
// Disabled controls are exempt (WCAG 1.4.3 "inactive user interface
// component").
async function contrastFailures(
  page: Page,
  rootTestId: string,
): Promise<{ checked: number; failures: ContrastFailure[] }> {
  return page.getByTestId(rootTestId).evaluate((root) => {
    const ctx = document.createElement("canvas").getContext("2d", {
      willReadFrequently: true,
    })!;
    type RGBA = [number, number, number, number];
    const parse = (css: string): RGBA => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = "#000";
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0], d[1], d[2], d[3] / 255];
    };
    const over = (top: RGBA, under: RGBA): RGBA => {
      const a = top[3];
      return [
        top[0] * a + under[0] * (1 - a),
        top[1] * a + under[1] * (1 - a),
        top[2] * a + under[2] * (1 - a),
        1,
      ];
    };
    const background = (el: Element): RGBA => {
      const layers: RGBA[] = [];
      for (let n: Element | null = el; n; n = n.parentElement) {
        const c = parse(getComputedStyle(n).backgroundColor);
        if (c[3] > 0) layers.push(c);
        if (c[3] >= 1) break;
      }
      let bg: RGBA = [255, 255, 255, 1];
      for (let i = layers.length - 1; i >= 0; i--) bg = over(layers[i], bg);
      return bg;
    };
    const luminance = ([r, g, b]: RGBA) => {
      const ch = (v: number) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * ch(r) + 0.7152 * ch(g) + 0.0722 * ch(b);
    };
    const hex = (c: RGBA) =>
      "#" +
      c
        .slice(0, 3)
        .map((v) => Math.round(v).toString(16).padStart(2, "0"))
        .join("");

    const failures: ContrastFailure[] = [];
    let checked = 0;
    const all = [root, ...Array.from(root.querySelectorAll("*"))];
    for (const el of all) {
      const ownText = Array.from(el.childNodes)
        .filter((n) => n.nodeType === Node.TEXT_NODE)
        .map((n) => n.textContent ?? "")
        .join("")
        .trim();
      if (!ownText) continue;
      const rect = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      if (rect.width === 0 || rect.height === 0) continue;
      if (style.visibility !== "visible" || style.display === "none") continue;
      if (el.closest(":disabled, [aria-disabled='true']")) continue;

      const bg = background(el);
      const fg = over(parse(style.color), bg);
      const l1 = luminance(fg);
      const l2 = luminance(bg);
      const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
      const size = parseFloat(style.fontSize);
      const bold = parseInt(style.fontWeight, 10) >= 700;
      const large = size >= 24 || (bold && size >= 18.66);
      const required = large ? 3 : 4.5;
      checked++;
      if (ratio < required) {
        failures.push({
          text: ownText.slice(0, 60),
          ratio: Math.round(ratio * 100) / 100,
          required,
          fg: hex(fg),
          bg: hex(bg),
        });
      }
    }
    return { checked, failures };
  });
}

async function relativeLuminance(page: Page, testId: string): Promise<number> {
  return page
    .getByTestId(testId)
    .first()
    .evaluate((el) => {
      const ctx = document.createElement("canvas").getContext("2d")!;
      ctx.fillStyle = getComputedStyle(el).backgroundColor;
      ctx.fillRect(0, 0, 1, 1);
      const [r, g, b] = Array.from(ctx.getImageData(0, 0, 1, 1).data);
      const ch = (v: number) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * ch(r) + 0.7152 * ch(g) + 0.0722 * ch(b);
    });
}

for (const theme of ["dark", "light"] as const) {
  test.describe(`board packs in ${theme} mode (#2057)`, () => {
    test.beforeEach(async ({ authedPage: page }) => {
      await useTheme(page, theme);
      await mockBoardPacks(page);
    });

    test("the list page meets WCAG AA contrast on the app theme", async ({
      authedPage: page,
    }) => {
      await page.goto("/board-packs");
      await expect(page.getByText("Review").first()).toBeVisible();
      expect(
        await page.evaluate(() =>
          document.documentElement.classList.contains("dark"),
        ),
      ).toBe(theme === "dark");

      const { checked, failures } = await contrastFailures(
        page,
        "board-packs-page",
      );
      expect(checked).toBeGreaterThan(10);
      expect(failures).toEqual([]);
    });

    for (const [label, id] of [
      ["draft", DRAFT_ID],
      ["published", PUBLISHED_ID],
    ] as const) {
      test(`the ${label} pack sheet renders light and meets WCAG AA contrast`, async ({
        authedPage: page,
      }) => {
        await page.goto(`/board-packs/${id}`);
        await expect(page.getByTestId("pack-header")).toBeVisible();
        await expect(page.getByTestId("posture-tiles")).toBeVisible();

        // Paper in every app theme: the sheet and its section cards are
        // light surfaces.
        expect(
          await relativeLuminance(page, "board-pack-view"),
        ).toBeGreaterThan(0.85);
        expect(
          await relativeLuminance(page, "section-card-posture"),
        ).toBeGreaterThan(0.95);

        const { checked, failures } = await contrastFailures(
          page,
          "board-pack-view",
        );
        expect(checked).toBeGreaterThan(50);
        expect(failures).toEqual([]);
      });
    }
  });
}
