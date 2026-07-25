// Slice 589 — Playwright E2E for the OSCAL vendor-claims view + the operator
// accept/reject/needs-info disposition.
//
// HERMETIC: every BFF route is browser-side `page.route`-mocked (the
// slice-594 b219 lesson — do NOT depend on a shared-DB seed). The spec
// asserts the client-side contract: the list + detail render the mocked
// payloads, the vendor-claim-is-assertion disclaimer is present, the unmapped
// flag surfaces, and an Accept click POSTs to the disposition BFF route.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/oscal-component-claims.spec.ts

import { expect, test } from "./fixtures";

const DEF_ID = "11111111-1111-1111-1111-111111111111";
const CLAIM_MAPPED = "22222222-2222-2222-2222-222222222222";
const CLAIM_UNMAPPED = "33333333-3333-3333-3333-333333333333";

const listBody = {
  component_definitions: [
    {
      id: DEF_ID,
      source_label: "Acme SaaS",
      catalog_title: "Acme Component Definition",
      oscal_version: "1.1.2",
      source_sha256: "abc",
      claim_count: 2,
      imported_by: "operator",
      imported_at: "2026-06-08T12:00:00Z",
    },
  ],
  count: 1,
};

const detailBody = {
  id: DEF_ID,
  source_label: "Acme SaaS",
  catalog_title: "Acme Component Definition",
  oscal_version: "1.1.2",
  source_sha256: "abc",
  imported_by: "operator",
  imported_at: "2026-06-08T12:00:00Z",
  claims: [
    {
      id: CLAIM_MAPPED,
      imported_component_id: "comp-1",
      component_uuid: "uuid-1",
      component_title: "Acme API",
      component_type: "service",
      control_id: "ac-2",
      statement: "Acme implements account management via SSO.",
      requirement_uuid: "req-1",
      scf_anchor_id: "SCF-IAC-06",
      unmapped: false,
      is_vendor_claim: true,
      claim_status: "asserted",
      disposition_note: "",
    },
    {
      id: CLAIM_UNMAPPED,
      imported_component_id: "comp-1",
      component_uuid: "uuid-1",
      component_title: "Acme API",
      component_type: "service",
      control_id: "ac-3",
      statement: "Acme enforces least privilege.",
      requirement_uuid: "req-2",
      scf_anchor_id: null,
      unmapped: true,
      is_vendor_claim: true,
      claim_status: "asserted",
      disposition_note: "",
    },
  ],
};

test.describe("OSCAL vendor-claims view", () => {
  test("AC-1: list renders imported component-definitions", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/oscal/component-definitions", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(listBody),
      }),
    );
    await page.goto("/oscal/component-definitions");
    await expect(page.getByTestId("component-definitions-page")).toBeVisible();
    await expect(page.getByTestId("component-definition-row")).toHaveCount(1);
    await expect(page.getByTestId("cd-source-label")).toHaveText("Acme SaaS");
    await expect(page.getByTestId("cd-claim-count")).toContainText("2 claims");
  });

  test("AC-2: detail renders claims with the unmapped flag + the assertion disclaimer", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/oscal/component-definitions/${DEF_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(detailBody),
      }),
    );
    await page.goto(`/oscal/component-definitions/${DEF_ID}`);
    await expect(page.getByTestId("component-definition-detail")).toBeVisible();
    // The claim-is-assertion-not-evidence boundary is surfaced honestly.
    await expect(page.getByTestId("vendor-claim-disclaimer")).toContainText(
      "not platform-verified evidence",
    );
    await expect(page.getByTestId("claim-row")).toHaveCount(2);
    // Each claim is labelled a vendor claim.
    await expect(page.getByTestId("claim-vendor-badge")).toHaveCount(2);
    // The unmapped claim surfaces the slice-512 NULL-anchor flag.
    await expect(page.getByTestId("claim-unmapped-badge")).toHaveCount(1);
    await expect(page.getByTestId("claim-scf-anchor").first()).toHaveText(
      "SCF-IAC-06",
    );
  });

  test("AC-3: Accept POSTs the disposition to the BFF route", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/oscal/component-definitions/${DEF_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(detailBody),
      }),
    );
    let dispositionPosted: { disposition?: string; note?: string } | null =
      null;
    await page.route(
      `**/api/oscal/component-claims/${CLAIM_MAPPED}/disposition`,
      async (route) => {
        dispositionPosted = JSON.parse(route.request().postData() ?? "{}") as {
          disposition?: string;
          note?: string;
        };
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            id: CLAIM_MAPPED,
            control_id: "ac-2",
            is_vendor_claim: true,
            claim_status: "accepted",
            dispositioned_by: "grc-1",
            dispositioned_at: "2026-06-08T12:05:00Z",
            disposition_note: "looks good",
          }),
        });
      },
    );

    await page.goto(`/oscal/component-definitions/${DEF_ID}`);
    await expect(page.getByTestId("claim-row").first()).toBeVisible();
    await page.getByTestId("claim-note-input").first().fill("looks good");
    await page.getByTestId("claim-accept").first().click();

    await expect
      .poll(() => dispositionPosted)
      .toEqual({ disposition: "accept", note: "looks good" });
  });

  // ===== Slice 620: map an unmapped claim to an SCF anchor =====

  const anchorsBody = {
    anchors: [
      {
        id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
        scf_id: "TST-01",
        family: "TST",
        name: "Test Anchor One",
        description: "first test anchor",
      },
      {
        id: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
        scf_id: "TST-02",
        family: "TST",
        name: "Test Anchor Two",
        description: "second test anchor",
      },
    ],
  };

  test("AC-5 (slice 620): the unmapped claim shows the SCF-anchor picker; the mapped claim does not", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/oscal/component-definitions/${DEF_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(detailBody),
      }),
    );
    await page.route("**/api/anchors", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(anchorsBody),
      }),
    );
    await page.goto(`/oscal/component-definitions/${DEF_ID}`);
    await expect(page.getByTestId("claim-row")).toHaveCount(2);
    // Exactly one claim (the unmapped one) renders the picker.
    await expect(page.getByTestId("scf-anchor-picker")).toHaveCount(1);
    await expect(page.getByTestId("claim-unmapped-badge")).toHaveCount(1);
  });

  test("AC-6 (slice 620): mapping an unmapped claim PATCHes the BFF + clears the unmapped badge", async ({
    authedPage: page,
  }) => {
    // The BFF GET is route-mocked (hermetic — slice-594 b219). First load
    // returns the unmapped claim; after the PATCH, the invalidated refetch
    // returns the now-mapped claim so the badge clears.
    const mappedDetail = JSON.parse(
      JSON.stringify(detailBody),
    ) as typeof detailBody;
    mappedDetail.claims[1].scf_anchor_id = "TST-02";
    mappedDetail.claims[1].unmapped = false;

    let mapped = false;
    await page.route(`**/api/oscal/component-definitions/${DEF_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mapped ? mappedDetail : detailBody),
      }),
    );
    await page.route("**/api/anchors", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(anchorsBody),
      }),
    );

    let patchBody: { scf_anchor_id?: string } | null = null;
    await page.route(
      `**/api/oscal/component-claims/${CLAIM_UNMAPPED}/scf-anchor`,
      async (route) => {
        patchBody = JSON.parse(route.request().postData() ?? "{}") as {
          scf_anchor_id?: string;
        };
        mapped = true;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            id: CLAIM_UNMAPPED,
            control_id: "ac-3",
            is_vendor_claim: true,
            claim_status: "asserted",
            scf_anchor_id: "TST-02",
            unmapped: false,
          }),
        });
      },
    );

    await page.goto(`/oscal/component-definitions/${DEF_ID}`);
    await expect(page.getByTestId("scf-anchor-picker")).toBeVisible();
    // Filter the picker to TST-02 and click the option.
    await page.getByTestId("scf-anchor-search").fill("TST-02");
    await page
      .getByTestId("scf-anchor-option")
      .filter({ hasText: "TST-02" })
      .click();

    // The PATCH carried the chosen anchor code.
    await expect.poll(() => patchBody).toEqual({ scf_anchor_id: "TST-02" });
    // After the invalidated refetch returns the mapped claim, the unmapped
    // badge is gone and the picker no longer renders for that claim.
    await expect(page.getByTestId("claim-unmapped-badge")).toHaveCount(0);
    await expect(page.getByTestId("scf-anchor-picker")).toHaveCount(0);
  });

  test("AC-7 (slice 620): a mapping error (422 unknown anchor) surfaces in the UI", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/oscal/component-definitions/${DEF_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(detailBody),
      }),
    );
    await page.route("**/api/anchors", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(anchorsBody),
      }),
    );
    await page.route(
      `**/api/oscal/component-claims/${CLAIM_UNMAPPED}/scf-anchor`,
      (route) =>
        route.fulfill({
          status: 422,
          contentType: "application/json",
          body: JSON.stringify({
            error: "scf_anchor_id does not resolve to a bundled SCF anchor",
          }),
        }),
    );
    await page.goto(`/oscal/component-definitions/${DEF_ID}`);
    await page.getByTestId("scf-anchor-option").first().click();
    await expect(page.getByTestId("disposition-error")).toContainText(
      "bundled SCF anchor",
    );
  });

  test("AC-4: a disposition error surfaces in the UI", async ({
    authedPage: page,
  }) => {
    await page.route(`**/api/oscal/component-definitions/${DEF_ID}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(detailBody),
      }),
    );
    await page.route(
      `**/api/oscal/component-claims/${CLAIM_MAPPED}/disposition`,
      (route) =>
        route.fulfill({
          status: 403,
          contentType: "application/json",
          body: JSON.stringify({
            error:
              "grc_engineer (approver) role required to disposition a vendor claim",
          }),
        }),
    );
    await page.goto(`/oscal/component-definitions/${DEF_ID}`);
    await page.getByTestId("claim-reject").first().click();
    await expect(page.getByTestId("disposition-error")).toContainText(
      "approver",
    );
  });
});
