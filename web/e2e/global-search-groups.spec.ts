// #2067 — Playwright e2e for the grouped ⌘K search popover: groups in
// the order controls, risks, policies, evidence, anchors, each with its
// match count, and "show all" for a group the shared limit cut short.
//
// `/api/search` is route-mocked (the controls-top-bar.spec.ts pattern).
// The grouping, ordering and counts are the upstream's job and are
// proven against real Postgres by internal/api/search's #2067
// integration tests; this spec pins what the operator sees. The
// real-endpoint path is covered by global-search.spec.ts, which also
// asserts its seeded groups' counts.
//
// Seeding a real policy into the shared e2e tenant is avoided on
// purpose: policy-review.spec.ts asserts the tenant's exact policy
// count, and fixtures are additive within a CI run.
//
// Run locally:
//   cd web
//   npx playwright test e2e/global-search-groups.spec.ts

import { expect, test } from "./fixtures";

const id = (n: number) =>
  `00000000-0000-0000-0000-${String(n).padStart(12, "0")}`;

function hit(n: number, type: string, title: string) {
  return { id: id(n), type, title, snippet: title, relevance_score: 1 };
}

// The merged response: one row per group (the shared limit cut the
// controls and anchors groups short), deliberately listed out of group
// order so the render order is the component's, not the payload's.
const MERGED = {
  hits: [
    hit(901, "anchors", "IAC-01 — Identity & Access Management"),
    hit(301, "policies", "Access Control Policy"),
    hit(101, "controls", "Access review — production"),
    hit(201, "risks", "Stale access"),
  ],
  count: 4,
  groups: [
    { type: "controls", count: 3 },
    { type: "risks", count: 1 },
    { type: "policies", count: 1 },
    { type: "evidence", count: 0 },
    { type: "anchors", count: 40 },
  ],
  partial_types: [],
};

const CONTROLS_ONLY = {
  hits: [
    hit(101, "controls", "Access review — production"),
    hit(102, "controls", "Access review — staging"),
    hit(103, "controls", "Privileged access logging"),
  ],
  count: 3,
  groups: [{ type: "controls", count: 3 }],
  partial_types: [],
};

test.describe("global search groups (#2067)", () => {
  test("results render grouped with counts, own records before the SCF catalog, and show all expands a group", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/search**", async (route) => {
      const url = new URL(route.request().url());
      const body =
        url.searchParams.get("types") === "controls" ? CONTROLS_ONLY : MERGED;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });

    await page.goto("/controls");
    const input = page.getByTestId("global-search-input");
    await expect(input).toBeVisible({ timeout: 30_000 });
    await input.click();
    await input.fill("access");

    const popover = page.getByTestId("global-search-popover");
    await expect(popover).toBeVisible({ timeout: 30_000 });

    // Group order: controls, risks, policies, anchors. Evidence has no
    // rows and renders no group.
    const groupIds = await popover
      .locator(
        '[data-testid^="global-search-group-"]:not([data-testid^="global-search-group-count-"])',
      )
      .evaluateAll((els) => els.map((el) => el.getAttribute("data-testid")));
    expect(groupIds).toEqual([
      "global-search-group-controls",
      "global-search-group-risks",
      "global-search-group-policies",
      "global-search-group-anchors",
    ]);
    await expect(page.getByTestId("global-search-group-evidence")).toHaveCount(
      0,
    );

    // Each group shows its full match count.
    await expect(
      page.getByTestId("global-search-group-count-controls"),
    ).toHaveText("3");
    await expect(
      page.getByTestId("global-search-group-count-risks"),
    ).toHaveText("1");
    await expect(
      page.getByTestId("global-search-group-count-policies"),
    ).toHaveText("1");
    await expect(
      page.getByTestId("global-search-group-count-anchors"),
    ).toHaveText("40");

    // The policy row is a real link to the policy detail page.
    await expect(
      page.getByTestId("global-search-row-policies").first(),
    ).toHaveAttribute("href", `/policies/${id(301)}`);

    // "Show all" appears only where the group holds fewer rows than it
    // matched.
    await expect(
      page.getByTestId("global-search-show-all-controls"),
    ).toHaveText("Show all 3");
    await expect(
      page.getByTestId("global-search-show-all-anchors"),
    ).toBeVisible();
    await expect(page.getByTestId("global-search-show-all-risks")).toHaveCount(
      0,
    );
    await expect(
      page.getByTestId("global-search-show-all-policies"),
    ).toHaveCount(0);

    // The first flat row is the control, so Enter would open it.
    await expect(input).toHaveAttribute(
      "aria-activedescendant",
      `global-search-option-controls-${id(101)}`,
    );

    // Show all fetches the controls group on its own and renders every
    // row; the button goes away once the group is complete.
    const showAllRequest = page.waitForRequest(
      (r) =>
        r.url().includes("/api/search") &&
        new URL(r.url()).searchParams.get("types") === "controls",
    );
    await page.getByTestId("global-search-show-all-controls").click();
    await showAllRequest;
    await expect(page.getByTestId("global-search-row-controls")).toHaveCount(3);
    await expect(
      page.getByTestId("global-search-show-all-controls"),
    ).toHaveCount(0);
    await expect(popover).toBeVisible();
  });
});
