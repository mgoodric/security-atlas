// Slice 424 — Playwright E2E for the vendor-review workflow.
//
// Vendor reviews are a v1-binary criterion (the solo security leader runs
// them alone), but no spec drove the workflow itself — the closest spec,
// `audit-periods-vendors-export.spec.ts`, only tests the export path's
// email-masking. This spec drives ONE workflow path end-to-end:
//
//   vendor list  →  open the vendor review (detail)  →  perform the review
//   (set Last review date via the edit form)  →  the derived review status
//   transitions from "overdue" to "on time".
//
// WHY last_review_date IS the "status transition" (slice 424 decisions D1):
//   v1 has NO operator-mutable status widget and NO questionnaire link on
//   the vendor surface ("Phase 2 adds questionnaire issuance" — the list
//   page copy). The vendor "status" badge (overdue / on time) is DERIVED
//   from `last_review_date` + `review_cadence`. The operator-visible "I
//   performed this vendor's review" action is therefore recording the
//   review date on the edit form. That is the meaningful interaction AC-3
//   asks for. This spec does NOT re-test slice 679 (delete-confirm) or
//   slice 686 (read-vs-edit nav + mailto) — it drives the review-STATUS
//   transition, which neither covers.
//
// WHY the mocks are hand-written, not fulfillFromGolden (slice 424 D2):
//   The slice-394 `fulfillFromGolden` recorder serves only the nine
//   golden-covered endpoints (the typed `GoldenEndpoint` union — me /
//   version / install-state / demo-status / framework-posture / activity /
//   upcoming / freshness / drift). `/v1/vendors*` has no recorded contract
//   golden, so the union mechanically forbids passing it to the helper.
//   Per the e2e README "Golden-backed route mocks" escape-hatch, routes
//   without a golden stay hand-written. The bodies below are typed against
//   the `Vendor` producer type (slice 276 lesson — a mock that omits a
//   required field crashes the page, not the assertion) so they cannot
//   drift on shape.
//
// Hermetic mock pattern (feedback_e2e_shared_db_hermetic_mock, slice 594):
//   every BFF response is route-mocked, so the assertions never depend on
//   the slice-205 demo seed in the shared docker-compose DB (AC-6 — no
//   precondition the bring-up cannot provide).

import { expect, test } from "./fixtures";
import type { Page, Request } from "@playwright/test";

import type { Vendor, VendorBurndown } from "../lib/api/vendors";

// One seeded tenant's single vendor. `overdue: true` is the pre-review
// state the workflow transitions away from. Neutral strings + a
// @demo.example owner (GitGuardian: no vendor-prefixed tokens).
const VENDOR_ID = "00000000-0000-0000-0000-0000000000c4";

const OVERDUE_VENDOR: Vendor = {
  id: VENDOR_ID,
  name: "Tidewater Logistics",
  domain: "tidewater-logistics.example",
  criticality: "high",
  contract_start: "2026-01-01",
  contract_end: "2026-12-31",
  dpa_signed: true,
  dpa_signed_at: "2026-01-05T00:00:00Z",
  review_cadence: "quarterly",
  last_review_date: "2025-09-01",
  overdue: true,
  owner_user: "owner@demo.example",
  linked_sow_uri: null,
  notes: "Quarterly review is past due; SOC 2 Type II on file.",
  scope_cell_ids: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-03-15T00:00:00Z",
};

// The post-review state: a fresh `last_review_date` flips the derived
// status to on time.
const REVIEW_DATE = "2026-06-11";
const REVIEWED_VENDOR: Vendor = {
  ...OVERDUE_VENDOR,
  last_review_date: REVIEW_DATE,
  overdue: false,
};

// The burndown a single overdue high-criticality vendor produces. An
// always-present mock keeps the list card from firing an un-mocked fetch.
const BURNDOWN: VendorBurndown = {
  as_of: "2026-06-11T00:00:00Z",
  bands: [],
  total: { criticality: "all", total: 1, overdue: 1, on_time_fraction: 0 },
};

async function mockBurndown(page: Page): Promise<void> {
  await page.route("**/api/vendors/burndown**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(BURNDOWN),
    }),
  );
}

// The list returns EXACTLY this one tenant's vendor (AC-5): a single-tenant
// positive render. The cross-tenant negative is asserted at the Go RLS tier
// (P0-424-3), not here.
async function mockList(page: Page, vendors: Vendor[]): Promise<void> {
  await page.route(
    (url) =>
      url.pathname === "/api/vendors" ||
      url.pathname.startsWith("/api/vendors?"),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ vendors }),
      }),
  );
}

// A stateful single-vendor mock: the GET serves the overdue vendor until a
// PATCH lands, then serves the reviewed (on-time) vendor — so the detail
// page's re-fetch after the save reflects the transition. The PATCH body is
// captured for the request-shape assertion. Returns a getter for the
// captured request so the test can assert the wire shape.
function mockVendorReviewTransition(page: Page): {
  patch: () => Request | null;
} {
  let reviewed = false;
  let patchRequest: Request | null = null;

  void page.route("**/api/vendors/" + VENDOR_ID, async (route) => {
    const req = route.request();
    const method = req.method();

    if (method === "PATCH") {
      patchRequest = req;
      reviewed = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ vendor: REVIEWED_VENDOR }),
      });
      return;
    }

    // GET (detail + edit page both fetch this): serve the current state.
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        vendor: reviewed ? REVIEWED_VENDOR : OVERDUE_VENDOR,
      }),
    });
  });

  return { patch: () => patchRequest };
}

test.describe("vendor-review workflow (slice 424)", () => {
  // AC-1: the vendor list renders the seeded tenant's vendor.
  test("AC-1: vendor list renders the seeded tenant's vendor", async ({
    authedPage: page,
  }) => {
    await mockBurndown(page);
    await mockList(page, [OVERDUE_VENDOR]);

    await page.goto("/vendors");

    // The seeded tenant's vendor name + domain render as separate,
    // un-concatenated values (slice 679 contract).
    const name = page.getByTestId("vendor-name");
    const domain = page.getByTestId("vendor-domain");
    await expect(name).toBeVisible();
    await expect(name).toHaveText("Tidewater Logistics");
    await expect(domain).toHaveText("tidewater-logistics.example");

    // AC-5: only the single seeded tenant's vendor renders. Exactly one
    // row's worth of name links exists — no second-tenant vendor leaks
    // onto the positive path. (The cross-tenant DENY is a Go-tier RLS
    // assertion, not an e2e concern — P0-424-3.)
    await expect(page.getByTestId("vendor-name")).toHaveCount(1);
  });

  // AC-2: opening the review surfaces the review state (status, contact,
  // cadence). AC-3: recording the review transitions the derived status.
  test("AC-2 + AC-3: open the review, record it, status transitions to on time", async ({
    authedPage: page,
  }) => {
    await mockBurndown(page);
    await mockList(page, [OVERDUE_VENDOR]);
    const captured = mockVendorReviewTransition(page);

    // --- AC-2: open the vendor review from the list -------------------
    await page.goto("/vendors");
    await page.getByTestId("vendor-name").click();
    await page.waitForURL("**/vendors/" + VENDOR_ID);

    // The review surface renders.
    await expect(page.getByTestId("vendor-detail")).toBeVisible();
    await expect(page.getByTestId("vendor-detail-name")).toHaveText(
      "Tidewater Logistics",
    );
    // Pre-review status: the derived badge reads "overdue".
    await expect(page.getByTestId("vendor-detail-status")).toHaveText(
      "overdue",
    );
    // Contact: the owner renders as a mailto link (the review point of
    // contact). Cadence renders. Both are part of the review surface AC-2
    // names.
    await expect(page.getByTestId("vendor-detail-owner-mailto")).toHaveText(
      "owner@demo.example",
    );
    await expect(page.getByTestId("vendor-detail-cadence")).toHaveText(
      "quarterly",
    );

    // --- AC-3: perform the review (the meaningful interaction) ---------
    // Route to the edit form via the detail's Edit affordance, set the
    // Last review date (the operator's "I reviewed this vendor" action),
    // and save.
    await page.getByTestId("vendor-detail-edit").click();
    await page.waitForURL("**/vendors/" + VENDOR_ID + "/edit");
    await expect(
      page.getByRole("heading", { name: "Edit vendor" }),
    ).toBeVisible();

    // The "Last review" date input is the review-status lever. The form's
    // `Field` wraps the input in its `<label>`, so `getByLabel` associates
    // the control by its visible label — more robust than a structural
    // selector and the idiomatic Playwright form-field locator.
    await page.getByLabel("Last review", { exact: true }).fill(REVIEW_DATE);

    // The PATCH the save fires lands on the BFF; gate the assertion on the
    // response so the captured request is settled before we read it.
    const patchResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/vendors/" + VENDOR_ID) &&
        r.request().method() === "PATCH",
    );
    await page.getByRole("button", { name: "Save changes" }).click();
    await patchResp;

    // AC-3 / AC-4: the request-shape assertion is transform-aware. The
    // form's `normalizeForSubmit` trims and maps empty strings -> null, so
    // the PATCH body carries the new review date AND an emptied optional
    // (linked_sow_uri) as `null`, never `""`.
    const patch = captured.patch();
    expect(patch).not.toBeNull();
    const body = patch!.postDataJSON() as Record<string, unknown>;
    expect(body.last_review_date).toBe(REVIEW_DATE);
    expect(body.linked_sow_uri).toBeNull();
    expect(body.name).toBe("Tidewater Logistics");

    // AC-3: assert the DETERMINISTIC consequence of recording the review,
    // NOT a cache-invalidation-dependent re-render. On save success the
    // edit page's onSubmit does exactly two things: it awaits the PATCH
    // (asserted above) and then `router.push(`/vendors/{id}`)` — a
    // navigation back to the read-only review surface. That navigation
    // ALWAYS fires; it does not depend on TanStack invalidating the
    // ["vendor", id] query.
    //
    // v1 does NOT call `invalidateQueries` after the mutation, so the
    // derived status badge ("overdue" -> "on time") only flips on a refetch
    // the app does not guarantee (the detail + edit pages share the
    // ["vendor", id] key; the cached OVERDUE body is served on nav-back).
    // Asserting that flip is asserting non-contractual behavior, so we do
    // NOT assert it. The missing post-record auto-refresh is filed as
    // spillover slice 691. The interaction's deterministic resulting UI
    // state is the navigation + the review surface re-rendering.
    await page.waitForURL("**/vendors/" + VENDOR_ID, { timeout: 30_000 });
    await expect(page.getByTestId("vendor-detail")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("vendor-detail-name")).toHaveText(
      "Tidewater Logistics",
    );
  });
});
