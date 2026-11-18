// #2135 — the dashboard as a front door (parent #2072).
//
// Pins, in the browser:
//   - the section order: Needs attention, Posture, Coming up, Recent
//     activity;
//   - For you shows the caller's items, Everyone the rest, and the
//     toggle appears only when /v1/me says the caller may see everyone;
//   - See all opens /needs-attention, which pages and filters by type;
//   - the top-risks card shows the 5 open, non-roll-up risks the BFF
//     returns, in its order, with band, owner and treatment;
//   - the audit banner counts the days to the open period's end and
//     links to the period's page;
//   - Recent activity renders sentences, not raw kinds;
//   - one failing section shows its own error and leaves the rest.
//
// The BFF answers are mocked with `page.route`, the dashboard.spec.ts
// pattern: the `e2e_no_prefetch` cookie makes the layout skip its SSR
// prefetch so the browser-side mocks reach the first render. What the
// platform itself returns for each view, type filter and tenant (two
// users and two tenants, with every needs-attention source) is pinned
// against real Postgres by the Go integration suite:
// internal/api/needs_attention_2134_integration_test.go, on the
// internal/dbtest/attention fixture. The mock below stands in for that
// server, so this spec checks the page asks for the right view and
// renders what it is given, without re-filtering.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const ME = "00000000-0000-4000-8000-0000000021a1";
const OTHER = "00000000-0000-4000-8000-0000000021a2";

type Item = {
  band: string;
  type: string;
  id: string;
  title: string;
  link: string;
  action: string;
  due_at?: string;
  since?: string;
  owner?: { user_id?: string; role?: string };
};

const item = (
  n: number,
  band: string,
  type: string,
  owner: string | null,
): Item => ({
  band,
  type,
  id: `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
  title: `${owner === ME ? "Mine" : owner ? "Theirs" : "Unowned"} ${type} ${n}`,
  link: `/${type === "action_plan" ? "action-plans" : `${type}s`}/${n}`,
  action:
    band === "approval" ? "Approve" : type === "control" ? "Attest" : "Review",
  due_at:
    band === "overdue" || band === "due_soon"
      ? "2026-11-01T00:00:00Z"
      : undefined,
  since:
    band === "overdue" || band === "due_soon"
      ? undefined
      : "2026-10-01T00:00:00Z",
  owner: owner ? { user_id: owner } : undefined,
});

// The caller's items, then everyone else's (and the ownership gaps the
// platform lists only in for=all).
const MINE: Item[] = [
  item(1, "failing", "control", ME),
  item(2, "approval", "exception", ME),
  item(3, "overdue", "policy", ME),
];
const THEIRS: Item[] = [
  item(4, "overdue", "risk", OTHER),
  item(5, "overdue", "vendor", OTHER),
  item(6, "overdue", "action_plan", OTHER),
  item(7, "due_soon", "exception", OTHER),
  item(8, "ownership_gap", "control", null),
  item(9, "ownership_gap", "risk", null),
];

function respond(
  items: Item[],
  view: "me" | "all",
  types: string[],
  limit: number,
  offset: number,
) {
  const typeCounts: Record<string, number> = {};
  for (const it of items) typeCounts[it.type] = (typeCounts[it.type] ?? 0) + 1;
  const kept = types.length
    ? items.filter((i) => types.includes(i.type))
    : items;
  const counts: Record<string, number> = {};
  for (const it of kept) counts[it.band] = (counts[it.band] ?? 0) + 1;
  return {
    for: view,
    items: kept.slice(offset, offset + limit),
    total: kept.length,
    counts,
    type_counts: typeCounts,
    limit,
    offset,
  };
}

type Mocks = {
  approver: boolean;
  // What the platform serves with `for` omitted.
  defaultView?: "me" | "all";
  mine?: Item[];
  needsAttentionStatus?: number;
  calls: string[];
};

async function mockNeedsAttention(page: Page, m: Mocks) {
  await page.route("**/api/me", (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        user_id: ME,
        tenant_id: "00000000-0000-4000-8000-0000000021t1",
        display_name: "Sam Operator",
        email: "sam@example.invalid",
        idp_subject: "",
        tenant_role: m.approver ? "grc_engineer" : "control_owner",
        time_zone: null,
        theme: null,
        is_admin: false,
        is_tenant_admin: false,
        is_approver: m.approver,
        has_tenant_role: true,
        owner_roles: [],
        roles: [m.approver ? "grc_engineer" : "control_owner"],
      }),
    }),
  );
  await page.route(/\/api\/needs-attention(\?|$)/, (route: Route) => {
    const url = new URL(route.request().url());
    m.calls.push(url.search);
    if (m.needsAttentionStatus) {
      return route.fulfill({
        status: m.needsAttentionStatus,
        contentType: "application/json",
        body: JSON.stringify({ error: "needs attention unavailable" }),
      });
    }
    const asked = url.searchParams.get("for");
    if (asked === "all" && !m.approver) {
      return route.fulfill({ status: 403, body: "{}" });
    }
    const view = (asked as "me" | "all" | null) ?? m.defaultView ?? "me";
    const mine = m.mine ?? MINE;
    const items = view === "all" ? [...mine, ...THEIRS] : mine;
    const types = (url.searchParams.get("type") ?? "")
      .split(",")
      .filter(Boolean);
    const limit = Number(url.searchParams.get("limit") ?? "10");
    const offset = Number(url.searchParams.get("offset") ?? "0");
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(respond(items, view, types, limit, offset)),
    });
  });
}

function ymd(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

const PERIOD_ID = "00000000-0000-4000-8000-0000000021b1";

async function mockAuditPeriod(page: Page, daysLeft: number) {
  const end = new Date();
  end.setDate(end.getDate() + daysLeft);
  await page.route(/\/api\/audits(\?|$)/, (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        audit_periods: [
          {
            id: PERIOD_ID,
            name: "SOC 2 Type II 2026",
            framework_version_id: "00000000-0000-4000-8000-0000000021b2",
            period_start: "2026-01-01",
            period_end: ymd(end),
            status: "open",
            created_by: "test",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        ],
        count: 1,
      }),
    }),
  );
}

const risk = (n: number, residual: number, treatment: string) => ({
  id: `00000000-0000-4000-8000-0000000021c${n}`,
  title: `Top risk ${n}`,
  description: "",
  category: "operational",
  methodology: "nist_800_30",
  inherent_score: { likelihood: 5, impact: 5 },
  treatment,
  owner_user_id: n === 1 ? ME : null,
  owner_unlinked: n === 2 ? "Former CISO" : "",
  treatment_owner: "",
  residual_score: { score: residual },
  accepter: "",
  instrument_reference: "",
  linked_control_ids: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
});

const TOP_RISKS = [
  risk(1, 20, "mitigate"),
  risk(2, 16, "untreated"),
  risk(3, 12, "transfer"),
  risk(4, 9, "avoid"),
  risk(5, 4, "mitigate"),
];

async function mockTopRisks(page: Page, calls: string[]) {
  await page.route("**/api/dashboard/risks", (route: Route) => {
    calls.push(route.request().url());
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ risks: TOP_RISKS, count: TOP_RISKS.length }),
    });
  });
}

async function mockActivity(page: Page, calls: string[]) {
  await page.route(/\/api\/activity\?/, (route: Route) => {
    calls.push(route.request().url());
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        entries: [
          {
            occurred_at: new Date().toISOString(),
            actor_id: ME,
            actor_name: "Sam Operator",
            tenant_id: "t",
            kind: "exception",
            target_type: "exception",
            target_id: "00000000-0000-4000-8000-0000000021d1",
            action: "approve",
            row_id: "row-1",
            payload_json: null,
          },
          {
            occurred_at: new Date().toISOString(),
            actor_id: ME,
            actor_name: "Sam Operator",
            tenant_id: "t",
            kind: "risk",
            target_type: "risk",
            target_id: "00000000-0000-4000-8000-0000000021d2",
            action: "treatment_changed",
            row_id: "row-2",
            payload_json: null,
          },
        ],
        next_cursor: "",
      }),
    });
  });
}

test.describe("dashboard front door (#2135)", () => {
  test.beforeEach(async ({ authedPage: page, baseURL }) => {
    await page.context().addCookies([
      {
        name: "e2e_no_prefetch",
        value: "1",
        url: baseURL ?? "http://localhost:3000",
      },
    ]);
  });

  test("sections are in order: needs attention, posture, coming up, recent activity", async ({
    authedPage: page,
  }) => {
    await mockNeedsAttention(page, { approver: true, calls: [] });
    await page.goto("/dashboard");
    const sections = page.getByTestId("dashboard-section");
    await expect(sections).toHaveCount(4);
    const order = await sections.evaluateAll((els) =>
      els.map((e) => e.getAttribute("data-section")),
    );
    expect(order).toEqual([
      "needs-attention",
      "posture",
      "coming-up",
      "recent-activity",
    ]);
    // Posture holds the framework tiles and the top-risks card.
    const posture = page.locator('[data-section="posture"]');
    await expect(posture.getByTestId("framework-posture-panel")).toBeVisible();
    await expect(posture.getByTestId("top-risks-panel")).toBeVisible();
    await expect(
      page.locator('[data-section="coming-up"]').getByTestId("upcoming-panel"),
    ).toBeVisible();
    await expect(
      page
        .locator('[data-section="recent-activity"]')
        .getByTestId("activity-feed-panel"),
    ).toBeVisible();
  });

  test("For you shows only my items; Everyone shows the rest", async ({
    authedPage: page,
  }) => {
    const m: Mocks = { approver: true, calls: [] };
    await mockNeedsAttention(page, m);
    await page.goto("/dashboard");
    const panel = page.getByTestId("needs-attention-panel");
    const rows = panel.getByTestId("needs-attention-row");

    // The first load leaves the view to the server and asks for 10.
    await expect(rows).toHaveCount(MINE.length);
    expect(m.calls[0]).toBe("?limit=10");
    await expect(panel.getByTestId("needs-attention-view-me")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    for (const it of MINE) {
      await expect(panel.locator(`[data-id="${it.id}"]`)).toBeVisible();
    }
    for (const it of THEIRS) {
      await expect(panel.locator(`[data-id="${it.id}"]`)).toHaveCount(0);
    }
    // Each row carries its type chip and its action.
    const first = rows.first();
    await expect(first.getByTestId("needs-attention-type")).toHaveText(
      "Control",
    );
    await expect(first.getByTestId("needs-attention-action")).toHaveText(
      "Attest",
    );
    await expect(first.getByTestId("needs-attention-action")).toHaveAttribute(
      "href",
      MINE[0].link,
    );

    await panel.getByTestId("needs-attention-view-all").click();
    await expect(rows).toHaveCount(MINE.length + THEIRS.length);
    expect(m.calls).toContain("?for=all&limit=10");
    for (const it of THEIRS) {
      await expect(panel.locator(`[data-id="${it.id}"]`)).toBeVisible();
    }
    await expect(panel.getByTestId("needs-attention-view-all")).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    await panel.getByTestId("needs-attention-view-me").click();
    await expect(rows).toHaveCount(MINE.length);
  });

  test("the Everyone toggle is hidden from a caller who may not see everyone", async ({
    authedPage: page,
  }) => {
    const m: Mocks = { approver: false, calls: [] };
    await mockNeedsAttention(page, m);
    await page.goto("/dashboard");
    const panel = page.getByTestId("needs-attention-panel");
    await expect(panel.getByTestId("needs-attention-row")).toHaveCount(
      MINE.length,
    );
    await expect(panel.getByTestId("needs-attention-view")).toHaveCount(0);
    expect(m.calls.some((c) => c.includes("for=all"))).toBe(false);
  });

  test("a user who owns nothing and may see everyone lands on Everyone", async ({
    authedPage: page,
  }) => {
    await mockNeedsAttention(page, {
      approver: true,
      defaultView: "all",
      mine: [],
      calls: [],
    });
    await page.goto("/dashboard");
    const panel = page.getByTestId("needs-attention-panel");
    await expect(panel.getByTestId("needs-attention-row")).toHaveCount(
      THEIRS.length,
    );
    await expect(panel.getByTestId("needs-attention-view-all")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  test("empty and error states stay inside the section", async ({
    authedPage: page,
  }) => {
    await mockNeedsAttention(page, { approver: false, mine: [], calls: [] });
    await page.goto("/dashboard");
    await expect(page.getByTestId("needs-attention-empty")).toHaveText(
      "Nothing needs you right now.",
    );

    await page.unroute(/\/api\/needs-attention(\?|$)/);
    await mockNeedsAttention(page, {
      approver: true,
      needsAttentionStatus: 500,
      calls: [],
    });
    await page.goto("/dashboard");
    await expect(page.getByTestId("needs-attention-panel-error")).toBeVisible();
    await expect(page.getByTestId("needs-attention-panel-retry")).toBeVisible();
    // The other sections still render.
    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    await expect(page.getByTestId("upcoming-panel")).toBeVisible();
    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
  });

  test("each other section shows its own error without blanking the page", async ({
    authedPage: page,
  }) => {
    await mockNeedsAttention(page, { approver: true, calls: [] });
    await page.route("**/api/dashboard/risks", (r) =>
      r.fulfill({ status: 500, body: "{}" }),
    );
    await page.route(/\/api\/activity\?/, (r) =>
      r.fulfill({ status: 500, body: "{}" }),
    );
    await page.route("**/api/dashboard/framework-posture", (r) =>
      r.fulfill({ status: 500, body: "{}" }),
    );
    await page.route(/\/api\/calendar(\?|$)/, (r) =>
      r.fulfill({ status: 500, body: "{}" }),
    );
    await page.goto("/dashboard");
    await expect(page.getByTestId("top-risks-panel-error")).toBeVisible();
    await expect(page.getByTestId("activity-feed-panel-error")).toBeVisible();
    await expect(
      page.getByTestId("framework-posture-panel-error"),
    ).toBeVisible();
    await expect(page.getByTestId("upcoming-panel-error")).toBeVisible();
    await expect(page.getByTestId("needs-attention-row").first()).toBeVisible();
  });

  test("See all opens the full page, which pages and filters by type", async ({
    authedPage: page,
  }) => {
    const m: Mocks = { approver: true, calls: [] };
    await mockNeedsAttention(page, m);
    await page.goto("/dashboard");
    await page
      .getByTestId("needs-attention-panel")
      .getByTestId("needs-attention-view-all")
      .click();
    await expect(
      page
        .getByTestId("needs-attention-panel")
        .getByTestId("needs-attention-row"),
    ).toHaveCount(MINE.length + THEIRS.length);
    await page.getByTestId("needs-attention-see-all").click();

    await expect(page).toHaveURL(/\/needs-attention\?for=all$/);
    const full = page.getByTestId("needs-attention-page");
    await expect(full).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Needs attention" }),
    ).toBeVisible();
    await expect(full.getByTestId("needs-attention-row")).toHaveCount(
      MINE.length + THEIRS.length,
    );
    await expect(full.getByTestId("needs-attention-range")).toHaveText(
      `1–${MINE.length + THEIRS.length} of ${MINE.length + THEIRS.length}`,
    );
    expect(m.calls).toContain("?for=all&limit=25");

    // Type filters are the server's: the page asks with ?type= and shows
    // each type's count.
    const controls = page.getByTestId("needs-attention-type-filter-control");
    await expect(controls).toHaveText("Control (2)");
    await controls.click();
    await expect(page).toHaveURL(/type=control/);
    await expect(full.getByTestId("needs-attention-row")).toHaveCount(2);
    expect(m.calls).toContain("?for=all&type=control&limit=25");
    for (const row of await full.getByTestId("needs-attention-row").all()) {
      await expect(row).toHaveAttribute("data-type", "control");
    }
    await page.getByTestId("needs-attention-type-filter-risk").click();
    await expect(full.getByTestId("needs-attention-row")).toHaveCount(4);

    // A type with nothing in it shows the empty state.
    await page.goto("/needs-attention?for=all&type=framework_scope");
    await expect(page.getByTestId("needs-attention-empty")).toHaveText(
      "Nothing of these types needs attention.",
    );
  });

  test("the full page pages through the list", async ({ authedPage: page }) => {
    const many: Item[] = Array.from({ length: 30 }, (_, i) =>
      item(100 + i, "overdue", "policy", ME),
    );
    const m: Mocks = { approver: false, mine: many, calls: [] };
    await mockNeedsAttention(page, m);
    await page.goto("/needs-attention");
    const full = page.getByTestId("needs-attention-page");
    await expect(full.getByTestId("needs-attention-row")).toHaveCount(25);
    await expect(full.getByTestId("needs-attention-range")).toHaveText(
      "1–25 of 30",
    );
    await expect(page.getByTestId("needs-attention-prev")).toBeDisabled();
    await page.getByTestId("needs-attention-next").click();
    await expect(full.getByTestId("needs-attention-row")).toHaveCount(5);
    await expect(full.getByTestId("needs-attention-range")).toHaveText(
      "26–30 of 30",
    );
    expect(m.calls).toContain("?limit=25&offset=25");
    await expect(page.getByTestId("needs-attention-next")).toBeDisabled();
    // No Everyone toggle for this caller on the full page either.
    await expect(page.getByTestId("needs-attention-view")).toHaveCount(0);
  });

  test("the full page shows an error with a retry", async ({
    authedPage: page,
  }) => {
    await mockNeedsAttention(page, {
      approver: true,
      needsAttentionStatus: 500,
      calls: [],
    });
    await page.goto("/needs-attention");
    await expect(page.getByTestId("needs-attention-error")).toBeVisible();
    await expect(page.getByTestId("needs-attention-retry")).toBeVisible();
  });

  test("the top-risks card shows the 5 open, non-roll-up risks by residual", async ({
    authedPage: page,
  }) => {
    const calls: string[] = [];
    await mockNeedsAttention(page, { approver: true, calls: [] });
    await mockTopRisks(page, calls);
    await page.goto("/dashboard");
    const card = page.getByTestId("top-risks-panel");
    const rows = card.getByTestId("top-risk-row");
    await expect(rows).toHaveCount(5);
    // Rendered in the server's order: the BFF asks for open, non-roll-up
    // risks by residual (route.test.ts pins the upstream query).
    expect(calls.length).toBeGreaterThan(0);
    const titles = await card.getByTestId("top-risk-title").allTextContents();
    expect(titles).toEqual(TOP_RISKS.map((r) => r.title));
    // Band, owner and treatment on each row.
    await expect(rows.nth(0).getByTestId("top-risk-band")).toHaveText("High");
    await expect(rows.nth(2).getByTestId("top-risk-band")).toHaveText("Medium");
    await expect(rows.nth(4).getByTestId("top-risk-band")).toHaveText("Low");
    await expect(rows.nth(1).getByTestId("top-risk-owner")).toHaveText(
      "Former CISO (unlinked)",
    );
    await expect(rows.nth(3).getByTestId("top-risk-owner")).toHaveText(
      "No owner",
    );
    for (const r of await rows.all()) {
      await expect(r.getByTestId("top-risk-treatment")).not.toBeEmpty();
    }
  });

  test("the audit banner shows the days remaining and links to the period page", async ({
    authedPage: page,
  }) => {
    await mockNeedsAttention(page, { approver: true, calls: [] });
    await mockAuditPeriod(page, 12);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/dashboard");
    const banner = page.getByTestId("in-progress-audit-pill");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("data-days-remaining", "12");
    await expect(page.getByTestId("in-progress-audit-countdown")).toHaveText(
      "Audit ends in 12 days",
    );
    await expect(banner).toHaveAttribute("href", `/audits/${PERIOD_ID}`);
  });

  test("recent activity shows sentences, not raw kinds", async ({
    authedPage: page,
  }) => {
    const calls: string[] = [];
    await mockNeedsAttention(page, { approver: true, calls: [] });
    await mockActivity(page, calls);
    await page.goto("/dashboard");
    const panel = page.getByTestId("activity-feed-panel");
    const sentences = panel.getByTestId("activity-feed-sentence");
    await expect(sentences).toHaveCount(2);
    await expect(sentences.nth(0)).toContainText("approved exception");
    await expect(sentences.nth(1)).toContainText(
      "changed the treatment of risk",
    );
    await expect(panel).not.toContainText("treatment_changed");
    await expect(panel).not.toContainText("evidence.ingested");
    expect(
      calls.some((u) => new URL(u).searchParams.get("limit") === "10"),
    ).toBe(true);
  });
});
