// Issue #1778 — SCF remap review page (/controls/remap).
//
// These specs shape the page's BFF routes with page.route, using a
// release A -> release B fixture held in memory so decisions change what
// the next read returns:
//
//   * the tabs count the proposals a reviewer can still decide, per class;
//     bulk-approve sends ONE call to the bulk endpoint and empties the
//     unchanged tab;
//   * resolving a split (pick a successor) and a reused_id (both meanings
//     shown, never approved by default, anchor picked by search) sends the
//     chosen anchor and takes the proposal off the page;
//   * a non-super-admin (the edge read answers 403) sees no edge
//     proposals; a super-admin sees them beside the proposal.
//
// The Postgres-backed path (RLS, the approve re-anchoring the control as a
// superseding revision, the edge read's super-admin gate) is covered by
// the Go integration tests for #1776 and #1777, and the BFF's credential
// forwarding by the vitest route tests.

import type { Page, Request } from "@playwright/test";

// The platform's response types are generated from docs/openapi.yaml
// (#2394); every mocked body below `satisfies` one, so a contract change
// fails typecheck here instead of leaving this spec green on a stale shape.
import type {
  EdgeRemapProposal,
  EdgeRemapProposalsResponse,
  RemapBulkResult,
  RemapDecision,
  RemapProposal,
  RemapProposalsResponse,
  SCFAnchor,
} from "../lib/api/remap";

import { expect, test } from "./fixtures";

const A = "test-2026.1";
const B = "test-2026.2";

function proposal(
  n: number,
  over: Partial<RemapProposal> & Pick<RemapProposal, "class">,
): RemapProposal {
  const id = `00000000-0000-4000-8000-00000000${String(1700 + n).padStart(
    4,
    "0",
  )}`;
  return {
    id,
    control_id: `00000000-0000-4000-8000-00000001${String(1700 + n).padStart(
      4,
      "0",
    )}`,
    bundle_id: `tenant-bundle-${n}`,
    title: `Control ${n}`,
    legacy_scf_id: "GOV-01",
    legacy_release: A,
    target_release: B,
    status: "open",
    successor_scf_ids: [],
    created_at: "2026-11-15T12:00:00Z",
    ...over,
  };
}

function seedProposals(): RemapProposal[] {
  return [
    proposal(1, {
      class: "unchanged",
      legacy_scf_id: "GOV-01",
      successor_scf_ids: ["GOV-01"],
    }),
    proposal(2, {
      class: "unchanged",
      legacy_scf_id: "GOV-02",
      successor_scf_ids: ["GOV-02"],
    }),
    proposal(3, {
      class: "renamed",
      legacy_scf_id: "AST-01",
      successor_scf_ids: ["AST-01"],
    }),
    proposal(4, {
      class: "split",
      title: "Privileged access is managed",
      legacy_scf_id: "IAC-21",
      successor_scf_ids: ["IAC-21.1", "IAC-21.2"],
    }),
    proposal(5, {
      class: "reused_id",
      title: "Authenticators are managed",
      legacy_scf_id: "IAC-10",
      successor_scf_ids: ["IAC-14"],
    }),
    proposal(6, { class: "no_successor", legacy_scf_id: "OPS-09" }),
    // Settled: not counted in any tab.
    proposal(7, {
      class: "unchanged",
      status: "rebound",
      successor_scf_ids: ["GOV-03"],
    }),
    proposal(8, {
      class: "split",
      status: "kept_legacy",
      successor_scf_ids: ["X-1", "X-2"],
    }),
  ];
}

const anchor = (
  scf_id: string,
  name: string,
  extra: Partial<SCFAnchor> = {},
): SCFAnchor => ({
  id: `anchor-${scf_id}`,
  scf_id,
  family: scf_id.split("-")[0] ?? "",
  name,
  description: `${name} (our wording).`,
  ...extra,
});

const CATALOG: Record<string, Record<string, SCFAnchor>> = {
  [A]: {
    "IAC-21": anchor("IAC-21", "Privileged Account Management"),
    "IAC-10": anchor("IAC-10", "Authenticator Management"),
  },
  [B]: {
    "IAC-21.1": anchor("IAC-21.1", "Privileged Account Inventory"),
    "IAC-21.2": anchor("IAC-21.2", "Privileged Session Monitoring"),
    // The reused code: in B, IAC-10 carries another control's lineage.
    "IAC-10": anchor("IAC-10", "Password-Based Authentication", {
      legacy_scf_id: "IAC-22",
    }),
    "IAC-14": anchor("IAC-14", "Authenticator Lifecycle"),
  },
};

const EDGES: EdgeRemapProposal[] = [
  {
    id: "edge-1",
    edge_id: "edge-row-1",
    framework_slug: "soc2",
    framework_version: "2017",
    requirement_id: "requirement-cc6-1",
    requirement_code: "CC6.1",
    relationship_type: "subset_of",
    strength: 1,
    mapping_tier: "canonical",
    legacy_anchor_id: "anchor-IAC-21",
    legacy_scf_id: "IAC-21",
    legacy_anchor_title: "Privileged Account Management",
    legacy_release: A,
    target_release: B,
    class: "split",
    status: "open",
    successor_scf_ids: ["IAC-21.1", "IAC-21.2"],
    created_at: "2026-11-15T12:00:00Z",
  },
];

// decided is the part of a RemapDecision every decision carries.
const decided = (p: RemapProposal, decision: string): RemapDecision => ({
  proposal_id: p.id,
  decision,
  bulk: false,
  bundle_id: p.bundle_id,
  legacy_control_id: p.control_id,
  legacy_anchor_id: `anchor-${p.legacy_scf_id}`,
  legacy_scf_id: p.legacy_scf_id,
  legacy_release: p.legacy_release,
  decided_by: "reviewer",
  decided_at: "2026-11-15T12:30:00Z",
});

type Fixture = {
  proposals: RemapProposal[];
  bulkCalls: number;
  approvals: { id: string; body: Record<string, unknown> }[];
};

async function mockRemap(
  page: Page,
  opts: { superAdmin: boolean },
): Promise<Fixture> {
  const fx: Fixture = {
    proposals: seedProposals(),
    bulkCalls: 0,
    approvals: [],
  };

  await page.route("**/api/admin/crosswalk-edge-remap-proposals*", (route) =>
    opts.superAdmin
      ? route.fulfill({
          status: 200,
          json: { proposals: EDGES } satisfies EdgeRemapProposalsResponse,
        })
      : route.fulfill({ status: 403, json: { error: "super_admin required" } }),
  );

  await page.route("**/api/controls/remap-proposals**", async (route) => {
    const req: Request = route.request();
    const url = new URL(req.url());
    const path = url.pathname;

    if (path.endsWith("/remap-proposals/anchors")) {
      const release = url.searchParams.get("release") ?? "";
      const cat = CATALOG[release] ?? {};
      const codes = url.searchParams.get("codes");
      if (codes !== null) {
        const anchors = Object.fromEntries(
          codes.split(",").map((c) => [c, cat[c] ?? null]),
        );
        return route.fulfill({ json: { scf_release: release, anchors } });
      }
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const hits = Object.values(cat).filter(
        (a) =>
          a.scf_id.toLowerCase().includes(q) ||
          a.name.toLowerCase().includes(q),
      );
      return route.fulfill({
        json: { scf_release: release, anchors: hits, truncated: false },
      });
    }

    if (path.endsWith("/remap-proposals/approve-unchanged")) {
      fx.bulkCalls++;
      const approved = fx.proposals.filter(
        (p) => p.class === "unchanged" && p.status === "open",
      );
      for (const p of approved) p.status = "approved";
      return route.fulfill({
        json: {
          approved_count: approved.length,
          approved: approved.map((p) => ({
            ...decided(p, "approved"),
            bulk: true,
            new_scf_id: p.successor_scf_ids[0],
            new_release: B,
          })),
          skipped: [],
        } satisfies RemapBulkResult,
      });
    }

    const decision = path.match(
      /remap-proposals\/([^/]+)\/(approve|reject|keep-legacy)$/,
    );
    if (decision) {
      const [, id, action] = decision;
      const p = fx.proposals.find((x) => x.id === id);
      if (!p)
        return route.fulfill({
          status: 404,
          json: { error: "remap proposal not found" },
        });
      const body = (req.postDataJSON() ?? {}) as Record<string, unknown>;
      if (action === "approve") {
        fx.approvals.push({ id: p.id, body });
        p.status = "approved";
      } else {
        p.status = action === "reject" ? "rejected" : "kept_legacy";
      }
      return route.fulfill({
        json: {
          ...decided(p, p.status),
          ...(action === "approve"
            ? { new_scf_id: String(body.scf_id ?? ""), new_release: B }
            : {}),
        } satisfies RemapDecision,
      });
    }

    return route.fulfill({
      json: {
        proposals: fx.proposals,
        count: fx.proposals.length,
        total: fx.proposals.length,
      } satisfies RemapProposalsResponse,
    });
  });

  return fx;
}

const count = (page: Page, c: string) =>
  page.getByTestId(`remap-tab-count-${c}`);

test.describe("SCF remap review", () => {
  test("counts per class; bulk-approve unchanged empties that tab in one call", async ({
    authedPage: page,
  }) => {
    const fx = await mockRemap(page, { superAdmin: false });
    await page.goto("/controls/remap");

    await expect(page.getByTestId("remap-review-page")).toBeVisible();
    await expect(count(page, "unchanged")).toHaveText("2");
    await expect(count(page, "renamed")).toHaveText("1");
    await expect(count(page, "split")).toHaveText("1");
    await expect(count(page, "reused_id")).toHaveText("1");
    await expect(count(page, "no_successor")).toHaveText("1");

    await expect(page.getByTestId("remap-proposal")).toHaveCount(2);
    await page.getByTestId("remap-bulk-approve").click();

    await expect(count(page, "unchanged")).toHaveText("0");
    await expect(page.getByTestId("remap-tab-empty")).toBeVisible();
    await expect(page.getByTestId("remap-proposal")).toHaveCount(0);
    expect(fx.bulkCalls).toBe(1);
    expect(fx.approvals).toHaveLength(0);
    // The other tabs are untouched.
    await expect(count(page, "split")).toHaveText("1");
  });

  test("resolving a split and a reused_id re-anchors those controls", async ({
    authedPage: page,
  }) => {
    const fx = await mockRemap(page, { superAdmin: false });
    await page.goto("/controls/remap");

    // Split: the legacy anchor beside both successors; approve needs a pick.
    await page.getByTestId("remap-tab-split").click();
    const split = page.getByTestId("remap-proposal");
    await expect(split).toHaveCount(1);
    await expect(split.getByTestId("remap-legacy-anchor-title")).toHaveText(
      "Privileged Account Management",
    );
    await expect(split.getByTestId("remap-successor-title")).toHaveText([
      "Privileged Account Inventory",
      "Privileged Session Monitoring",
    ]);
    await expect(split.getByTestId("remap-approve")).toBeDisabled();
    await split.getByRole("radio", { name: "Choose IAC-21.2" }).check();
    await split.getByTestId("remap-approve").click();
    await expect(count(page, "split")).toHaveText("0");

    // Reused ID: flagged, both meanings shown, never approved by default.
    await page.getByTestId("remap-tab-reused_id").click();
    const reused = page.getByTestId("remap-proposal");
    await expect(reused.getByTestId("remap-reused-flag")).toBeVisible();
    await expect(reused.getByTestId("remap-reused-old")).toHaveText(
      "Authenticator Management",
    );
    await expect(reused.getByTestId("remap-reused-new")).toHaveText(
      "Password-Based Authentication",
    );
    await expect(reused.getByTestId("remap-reused-meanings")).toContainText(
      "continues IAC-22",
    );
    await expect(reused.getByTestId("remap-approve")).toBeDisabled();

    // Pick any anchor in release B through the search.
    await reused.getByTestId("remap-anchor-search").fill("lifecycle");
    await reused.getByRole("button", { name: "Search" }).click();
    await reused
      .getByTestId("remap-anchor-result")
      .filter({ hasText: "IAC-14" })
      .click();
    await expect(reused.getByTestId("remap-chosen")).toContainText("IAC-14");
    await reused.getByTestId("remap-approve").click();
    await expect(count(page, "reused_id")).toHaveText("0");

    expect(fx.approvals).toEqual([
      {
        id: "00000000-0000-4000-8000-000000001704",
        body: { scf_id: "IAC-21.2" },
      },
      {
        id: "00000000-0000-4000-8000-000000001705",
        body: { scf_id: "IAC-14" },
      },
    ]);
  });

  test("a non-super-admin sees no edge proposals", async ({
    authedPage: page,
  }) => {
    await mockRemap(page, { superAdmin: false });
    const edges = page.waitForResponse((r) =>
      r.url().includes("/api/admin/crosswalk-edge-remap-proposals"),
    );
    await page.goto("/controls/remap");
    expect((await edges).status()).toBe(403);
    await page.getByTestId("remap-tab-split").click();
    await expect(page.getByTestId("remap-proposal")).toHaveCount(1);
    await expect(page.getByTestId("remap-edges")).toHaveCount(0);
    await expect(page.getByTestId("remap-edge")).toHaveCount(0);
    await expect(page.getByTestId("remap-edges-error")).toHaveCount(0);
  });

  test("a super-admin sees the edge proposals on the anchor", async ({
    authedPage: page,
  }) => {
    await mockRemap(page, { superAdmin: true });
    await page.goto("/controls/remap");
    await page.getByTestId("remap-tab-split").click();
    const edge = page.getByTestId("remap-edge");
    await expect(edge).toHaveCount(1);
    await expect(edge).toContainText("soc2:2017 CC6.1");
  });
});
