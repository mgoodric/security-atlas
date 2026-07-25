// Slice 662 — Playwright E2E for the board-pack §05 vendor-burndown
// section render + the human-label publish blocker.
//
// Hermetic mock pattern (per the feedback_e2e_shared_db_hermetic_mock
// lesson, slice 594): this spec route-mocks the BFF GET for the pack and
// the /api/admin/me approver probe, so the assertions do not depend on the
// slice-205 demo seed being present in the shared docker-compose DB. The
// `authedPage` fixture supplies the session cookie that gets past the
// (authed) layout proxy; every DATA response is fulfilled by the mock.
//
// Two scenarios:
//   1. A pack carrying all eight sections renders §05 (Vendor risk
//      burndown) with its header, title, and structured visual — and the
//      section numbering does NOT jump §04 -> §06 (the original bug).
//   2. A pack MISSING the vendor_burndown section renders the graceful
//      "not generated" state for §05, and the "not ready to publish"
//      blocker shows the human label "Vendor risk burndown" — never the
//      raw key `vendor_burndown`.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const PACK_ID = "00000000-0000-0000-0000-0000006620aa";

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

const ORDERED_KEYS = [
  "posture",
  "top_risks",
  "coverage_trend",
  "open_findings",
  "vendor_burndown",
  "operational_metrics",
  "investment",
  "asks",
];

function section(key: string, approved: boolean) {
  const data: Record<string, unknown> =
    key === "vendor_burndown"
      ? {
          vendor_burndown_total: 8,
          vendor_burndown_on_time: 6,
          vendor_burndown_past_due: 2,
          vendor_burndown_on_time_pct: 75,
          vendor_burndown_on_time_fraction: 0.75,
        }
      : {};
  return {
    key,
    title: SECTION_LABELS[key],
    templated_text: `Templated narrative for ${SECTION_LABELS[key]}.`,
    override_text: "",
    approved,
    data,
  };
}

function buildPack(opts: { omit?: string[]; approvedAll?: boolean }) {
  const omit = new Set(opts.omit ?? []);
  const sections: Record<string, unknown> = {};
  for (const key of ORDERED_KEYS) {
    if (omit.has(key)) continue;
    sections[key] = section(key, opts.approvedAll ?? true);
  }
  return {
    id: PACK_ID,
    period_end: "2026-03-31",
    status: "draft",
    narrative_md: "Quarterly board pack — e2e mock.",
    created_at: "2026-03-31T00:00:00Z",
    updated_at: "2026-03-31T00:00:00Z",
    content: {
      period_end: "2026-03-31",
      generated_at: "2026-03-31T00:00:00Z",
      status: "draft",
      sections,
    },
  };
}

async function mockApprover(page: Page, isAdmin: boolean): Promise<void> {
  await page.route("**/api/admin/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ is_admin: isAdmin }),
    }),
  );
}

async function gotoPack(page: Page): Promise<void> {
  const resp = page.waitForResponse(
    (r) =>
      r.url().includes(`/api/board-packs/${PACK_ID}`) && r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto(`/board-packs/${PACK_ID}`);
  await resp;
}

test.describe("board-pack §05 vendor burndown (slice 662)", () => {
  test("AC-1: a complete pack renders §05 with its title + visual, no §04->§06 jump", async ({
    authedPage: page,
  }) => {
    await mockApprover(page, true);
    await page.route(`**/api/board-packs/${PACK_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(buildPack({})),
      }),
    );

    await gotoPack(page);

    // §05 renders with its card, header label, and the vendor-burndown
    // structured visual — the original bug was a blank §05 (default:null).
    const card = page.getByTestId("section-card-vendor_burndown");
    await expect(card).toBeVisible();
    await expect(card).toContainText("Vendor risk burndown");
    await expect(page.getByTestId("vendor-burndown-panel")).toBeVisible();
    await expect(page.getByTestId("vendor-burndown-on-time")).toContainText(
      "6/8",
    );

    // Numbering is contiguous: open_findings (§04) is immediately
    // followed by vendor_burndown (§05), which is followed by
    // operational_metrics (§06). No slot is dropped.
    await expect(page.getByTestId("section-card-open_findings")).toBeVisible();
    await expect(
      page.getByTestId("section-card-operational_metrics"),
    ).toBeVisible();
    await expect(card).toContainText("§ 05");

    // No raw key is ever shown on the page.
    await expect(page.getByTestId("board-pack-view")).not.toContainText(
      "vendor_burndown",
    );
  });

  test("AC-3 + AC-2: a pack missing §05 renders the graceful state and the blocker shows the human label, never the raw key", async ({
    authedPage: page,
  }) => {
    await mockApprover(page, true);
    await page.route(`**/api/board-packs/${PACK_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        // vendor_burndown omitted AND no section approved -> the publish
        // blocker lists every section's HUMAN label.
        body: JSON.stringify(
          buildPack({ omit: ["vendor_burndown"], approvedAll: false }),
        ),
      }),
    );

    await gotoPack(page);

    // The missing §05 slot renders the graceful "not generated" state
    // (no crash, no dropped slot) under its human label.
    const missing = page.getByTestId("section-missing-vendor_burndown");
    await expect(missing).toBeVisible();
    await expect(missing).toContainText("Vendor risk burndown");
    await expect(missing).toContainText("not generated");
    await expect(missing).toContainText("§ 05");

    // The publish blocker shows the human label, never the raw key.
    await expect(page.getByTestId("board-pack-view")).toContainText(
      "Vendor risk burndown",
    );
    await expect(page.getByTestId("board-pack-view")).not.toContainText(
      "vendor_burndown",
    );
  });
});
