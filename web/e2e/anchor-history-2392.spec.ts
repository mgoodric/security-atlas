// #2392 — anchor history on the control detail page, and the legacy-anchor
// flag on the controls list.
//
// One in-memory fixture backs every BFF the flow reads, so a decision made
// on the remap review page changes what the next read returns, as the
// platform does:
//
//   * a reviewer keeps a control's legacy anchor on /controls/remap; the
//     control's History tab then lists that decision (code@release, the
//     reviewer by name) above an older system decision, which reads
//     "System", and shows no id;
//   * the controls list flags the two controls with an undecided proposal;
//     after one is decided, the list flags only the other.
//
// HERMETIC: route-mocked (slice 594 convention). The platform side (the
// list's legacy_anchor field and filter, the anchor-history read, its
// tenant scoping, paging and role gate) is pinned by the Go integration
// tests in internal/api/controls/legacy_anchor_integration_test.go and
// internal/api/controldetail/anchor_history_integration_test.go.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page, Route } from "@playwright/test";

import type { ControlAnchorDecision } from "../lib/api/control-detail";
import type { RemapProposal } from "../lib/api/remap";

import { expect, test } from "./fixtures";

const A = "test-2026.1";
const B = "test-2026.2";
const REVIEWER = "00000000-0000-0000-0000-0000000023b1";
const CTRL_OPEN = "00000000-0000-4000-8000-000000002391";
const CTRL_REJECTED = "00000000-0000-4000-8000-000000002392";
const CTRL_SETTLED = "00000000-0000-4000-8000-000000002393";
// The #2140 raw-value scan (control-detail-layout-2140.spec.ts): no UUID,
// hash prefix or snake_case enum key is page text.
const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
const HASH_RE = /\b(?=[0-9a-f]*[a-f])(?=[0-9a-f]*\d)[0-9a-f]{7,}\b/i;
const ENUM_KEY_RE = /\b[a-z0-9]+_[a-z0-9_]+\b/;

type Fixture = {
  proposals: RemapProposal[];
  // Anchor history by control id, newest first.
  history: Record<string, ControlAnchorDecision[]>;
};

function proposal(
  n: number,
  controlId: string,
  title: string,
  status: RemapProposal["status"],
): RemapProposal {
  return {
    id: `00000000-0000-4000-8000-0000000023a${n}`,
    control_id: controlId,
    bundle_id: `bundle-2392-${n}`,
    title,
    legacy_scf_id: "OPS-09",
    legacy_release: A,
    target_release: B,
    class: "no_successor",
    status,
    successor_scf_ids: [],
    created_at: "2026-11-15T12:00:00Z",
  };
}

function newFixture(): Fixture {
  return {
    proposals: [
      proposal(1, CTRL_OPEN, "Incident runbooks are tested", "open"),
      proposal(2, CTRL_REJECTED, "Backups are restored quarterly", "rejected"),
      proposal(3, CTRL_SETTLED, "Change tickets are approved", "kept_legacy"),
    ],
    history: {
      [CTRL_OPEN]: [
        {
          id: "00000000-0000-4000-8000-0000000023c1",
          decision: "rejected",
          bulk: false,
          legacy_control_id: CTRL_OPEN,
          legacy_version: 1,
          legacy_scf_id: "OPS-09",
          legacy_release: A,
          new_control_id: null,
          new_version: null,
          new_scf_id: null,
          new_release: null,
          reason: "Waiting on the new release notes",
          decided_by: "system:scf-import",
          decided_at: "2026-11-14T09:00:00Z",
        },
      ],
    },
  };
}

const json = (body: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
});

async function mockAll(page: Page, fx: Fixture) {
  await page.route(/\/api\/people(\?.*)?$/, (route: Route) =>
    route.fulfill(
      json({
        users: [
          { id: REVIEWER, display_name: "Rhea Reviewer", status: "active" },
        ],
        api_keys: [],
      }),
    ),
  );
  await page.route("**/api/scope-cells**", (route) =>
    route.fulfill(json({ cells: [] })),
  );
  await page.route("**/api/admin/crosswalk-edge-remap-proposals*", (route) =>
    route.fulfill(json({ error: "super_admin required" }, 403)),
  );
  await page.route(/\/api\/evidence\?/, (route) =>
    route.fulfill(json({ evidence: [], count: 0, total: 0, next_cursor: "" })),
  );
  await page.route(/\/api\/exceptions\?/, (route) =>
    route.fulfill(json({ exceptions: [], count: 0 })),
  );

  await page.route("**/api/controls**", async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;

    // The controls list: legacy_anchor is the platform's field, computed
    // from the proposals (open or rejected flags a control).
    if (path === "/api/controls-list") {
      const rows = fx.proposals.map((p) => ({
        id: p.control_id,
        title: p.title,
        control_family: "Operations",
        scf_id: p.legacy_scf_id,
        lifecycle_state: "active",
        bundle_id: p.bundle_id,
        scf_anchor_id: null,
        state: null,
        frameworks: [],
        anchor_fit: "exact",
        anchor_fit_note: null,
        legacy_anchor: p.status === "open" || p.status === "rejected",
      }));
      return route.fulfill(
        json({ controls: rows, count: rows.length, total: rows.length }),
      );
    }
    if (path === "/api/controls") {
      return route.fulfill(
        json({
          anchors: [],
          total: 0,
          catalog_total: 0,
          with_control_total: 3,
        }),
      );
    }

    // The remap review page.
    if (path.startsWith("/api/controls/remap-proposals")) {
      const m = path.match(
        /remap-proposals\/([^/]+)\/(approve|reject|keep-legacy)$/,
      );
      if (m) {
        const p = fx.proposals.find((x) => x.id === m[1]);
        if (!p) return route.fulfill(json({ error: "not found" }, 404));
        const decision = m[2] === "reject" ? "rejected" : "kept_legacy";
        p.status = decision;
        const body = (req.postDataJSON() ?? {}) as { reason?: string };
        (fx.history[p.control_id] ??= []).unshift({
          id: `00000000-0000-4000-8000-0000000023d${
            fx.history[p.control_id].length
          }`,
          decision,
          bulk: false,
          legacy_control_id: p.control_id,
          legacy_version: 1,
          legacy_scf_id: p.legacy_scf_id,
          legacy_release: p.legacy_release,
          new_control_id: null,
          new_version: null,
          new_scf_id: null,
          new_release: null,
          reason: body.reason ?? null,
          decided_by: REVIEWER,
          decided_at: "2026-11-15T12:30:00Z",
        });
        return route.fulfill(
          json({ proposal_id: p.id, decision, decided_by: REVIEWER }),
        );
      }
      if (path.endsWith("/anchors")) {
        return route.fulfill(json({ scf_release: B, anchors: {} }));
      }
      return route.fulfill(
        json({ proposals: fx.proposals, count: fx.proposals.length }),
      );
    }

    // The control detail page, for CTRL_OPEN.
    const detail = `/api/controls/${CTRL_OPEN}`;
    if (path === `${detail}/coverage`) {
      return route.fulfill(
        json({
          control: {
            id: CTRL_OPEN,
            bundle_id: "bundle-2392-1",
            version: "1.0.0",
            scf_anchor_id: null,
            title: "Incident runbooks are tested",
            control_family: "Operations",
            implementation_type: "manual_periodic",
            lifecycle_state: "active",
            owner_role: "Security Engineering",
            freshness_class: "quarterly",
          },
          anchor: null,
          requirements: [],
        }),
      );
    }
    if (path === `${detail}/state`) {
      return route.fulfill(
        json({ control_id: CTRL_OPEN, states: [], count: 0 }),
      );
    }
    if (path === `${detail}/history`) {
      return route.fulfill(
        json({ control_id: CTRL_OPEN, history: [], count: 0, next_cursor: "" }),
      );
    }
    if (path === `${detail}/anchor-history`) {
      const decisions = fx.history[CTRL_OPEN] ?? [];
      return route.fulfill(
        json({
          control_id: CTRL_OPEN,
          decisions,
          count: decisions.length,
          next_cursor: "",
        }),
      );
    }
    if (path.startsWith(detail)) {
      return route.fulfill(json({ error: "not in this fixture" }, 404));
    }
    return route.fallback();
  });
}

async function keepLegacy(page: Page, title: string) {
  await page.goto("/controls/remap");
  // The page opens on the unchanged tab; the fixture's proposals are
  // no_successor.
  await page.getByTestId("remap-tab-no_successor").click({ timeout: 30_000 });
  const card = page.getByTestId("remap-proposal").filter({ hasText: title });
  await expect(card).toBeVisible({ timeout: 30_000 });
  await card.getByTestId("remap-keep-legacy").click();
  await expect(card).toHaveCount(0);
}

test.describe("anchor history and the legacy-anchor flag (#2392)", () => {
  test("the History tab lists a remap decision once a reviewer makes it", async ({
    authedPage: page,
  }) => {
    const fx = newFixture();
    await mockAll(page, fx);

    await page.goto(`/controls/${CTRL_OPEN}?tab=history`);
    const section = page.getByTestId("anchor-history-section");
    const entries = section.getByTestId("anchor-history-entry");
    await expect(entries).toHaveCount(1, { timeout: 30_000 });
    await expect(entries.first()).toContainText("System");

    await keepLegacy(page, "Incident runbooks are tested");

    await page.goto(`/controls/${CTRL_OPEN}?tab=history`);
    await expect(entries).toHaveCount(2, { timeout: 30_000 });
    const latest = entries.first();
    await expect(latest).toHaveAttribute("data-decision", "kept_legacy");
    await expect(latest).toContainText("Kept legacy anchor");
    await expect(latest.getByTestId("anchor-history-legacy")).toHaveText(
      `OPS-09@${A}`,
    );
    await expect(latest.getByTestId("anchor-history-decided-by")).toHaveText(
      "Rhea Reviewer",
    );
    const older = entries.nth(1);
    await expect(older).toHaveAttribute("data-decision", "rejected");
    await expect(older.getByTestId("anchor-history-decided-by")).toHaveText(
      "System",
    );
    await expect(older.getByTestId("anchor-history-reason")).toHaveText(
      "Waiting on the new release notes",
    );
    const text = await section.innerText();
    expect(text, "anchor history shows a hash prefix").not.toMatch(HASH_RE);
    expect(text, "anchor history shows an enum key").not.toMatch(ENUM_KEY_RE);
    expect(text, "anchor history shows a UUID").not.toMatch(UUID_RE);
  });

  test("the controls list flags undecided controls and clears a decided one", async ({
    authedPage: page,
  }) => {
    const fx = newFixture();
    await mockAll(page, fx);

    const table = page.getByTestId("list-table-wrap");
    const flags = table.getByTestId("controls-row-legacy-anchor");
    const flaggedRow = (title: string) =>
      table.getByTestId("list-table-row").filter({ hasText: title });

    await page.goto("/controls");
    await expect(table.getByTestId("list-table-row")).toHaveCount(3, {
      timeout: 30_000,
    });
    await expect(flags).toHaveCount(2);
    await expect(
      flaggedRow("Incident runbooks are tested").getByTestId(
        "controls-row-legacy-anchor",
      ),
    ).toBeVisible();
    await expect(
      flaggedRow("Change tickets are approved").getByTestId(
        "controls-row-legacy-anchor",
      ),
    ).toHaveCount(0);
    await expect(flags.first()).toHaveAttribute("href", "/controls/remap");

    await keepLegacy(page, "Incident runbooks are tested");

    await page.goto("/controls");
    await expect(table.getByTestId("list-table-row")).toHaveCount(3, {
      timeout: 30_000,
    });
    await expect(flags).toHaveCount(1);
    await expect(
      flaggedRow("Backups are restored quarterly").getByTestId(
        "controls-row-legacy-anchor",
      ),
    ).toBeVisible();
    await expect(
      flaggedRow("Incident runbooks are tested").getByTestId(
        "controls-row-legacy-anchor",
      ),
    ).toHaveCount(0);
  });
});
