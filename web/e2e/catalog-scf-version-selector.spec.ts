// Slice 484 / ADR 0019 — Playwright E2E for the SCF anchor-detail
// framework-version selector (AC-6). When an anchor's requirements span more
// than one framework version, the operator can pick which version's
// requirements to view; selecting a version re-queries the BFF with the
// `?framework_version=slug:version` pin.
//
// HERMETIC: route-mocks the `/api/anchors/{id}/requirements` BFF GET so it does
// not depend on the shared docker-compose seed (slice 594 shared-DB →
// hermetic-mock convention). The mock returns DIFFERENT requirement sets for
// the unpinned (current) read and the pinned (legacy version) read, proving the
// selector drives a version-pinned re-fetch with no cross-version bleed.

import { expect, test } from "./fixtures";

const ANCHOR_ID = "IAC-06";

// The unpinned (current-versions) payload: SOC 2 2017 (current) carries CC6.1.
function currentPayload() {
  return {
    anchor: {
      id: "00000000-0000-4000-8000-0000000000a1",
      scf_id: "IAC-06",
      family: "IAC",
      name: "Multi-Factor Authentication",
      description: "Mock anchor for slice 484 e2e.",
    },
    requirements: [
      {
        requirement: {
          id: "00000000-0000-4000-8000-000000000011",
          framework_version_id: "fv-soc2-2017",
          code: "CC6.1",
          text: "Logical access controls — current version row.",
        },
        framework_version: {
          id: "fv-soc2-2017",
          framework: "SOC2",
          version: "2017",
        },
        strm_type: "equal",
        strength: 0.9,
      },
    ],
  };
}

// The pinned payload for the synthetic adjacent revision: a DIFFERENT row,
// proving the pin re-fetches the other version with no bleed.
function pinnedPayload() {
  return {
    anchor: currentPayload().anchor,
    requirements: [
      {
        requirement: {
          id: "00000000-0000-4000-8000-000000000022",
          framework_version_id: "fv-soc2-rev",
          code: "CC6.1b",
          text: "Logical access controls — synthetic adjacent revision row.",
        },
        framework_version: {
          id: "fv-soc2-rev",
          framework: "SOC2",
          version: "2017-synthetic-rev",
        },
        strm_type: "equal",
        strength: 0.9,
      },
    ],
  };
}

test.describe("SCF anchor-detail framework-version selector (slice 484)", () => {
  test("selecting a version re-queries with the pin and renders that version", async ({
    authedPage,
  }) => {
    // Route-mock: the unpinned read returns the current payload, but the
    // current payload's option list must include BOTH versions so the
    // selector renders. We surface both versions in the unpinned response so
    // the dropdown is populated, then assert the pinned re-fetch on selection.
    const both = currentPayload();
    both.requirements.push(pinnedPayload().requirements[0]);

    await authedPage.route(
      /\/api\/anchors\/[^/]+\/requirements(\?.*)?$/,
      async (route, request) => {
        const url = new URL(request.url());
        const pin = url.searchParams.get("framework_version");
        const body = pin === "soc2:2017-synthetic-rev" ? pinnedPayload() : both;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(body),
        });
      },
    );

    await authedPage.goto(`/catalog/scf/${ANCHOR_ID}`);

    // The selector is present (more than one framework version exists).
    const selector = authedPage.getByTestId("framework-version-select");
    await expect(selector).toBeVisible();

    // Default (All current versions) shows the current-version row.
    await expect(
      authedPage.getByText("Logical access controls — current version row."),
    ).toBeVisible();

    // Pick the synthetic adjacent revision → the pinned re-fetch renders the
    // other version's distinct requirement (no cross-version bleed).
    await selector.selectOption("soc2:2017-synthetic-rev");

    await expect(
      authedPage.getByText(
        "Logical access controls — synthetic adjacent revision row.",
      ),
    ).toBeVisible();
  });
});
