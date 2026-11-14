// #1795 — the applicability diff on the framework-scope review screen.
//
// Seeded by fixtures/e2e/framework-scope-diff.sql: a framework version
// (`2026-diff-e2e`) with four requirements DIFF-01..DIFF-04 and no scope
// revisions. beforeAll builds, through the API:
//
//   rev A (activated): DIFF-01 excluded, DIFF-02 excluded ("No card data
//     stored"), DIFF-03 applicable, DIFF-04 undecided; predicate true
//   rev B (in review): DIFF-01 applicable      -> decision changed
//                      DIFF-02 excluded ("Card data is tokenised at the
//                        edge")                 -> justification changed
//                      DIFF-03 applicable      -> unchanged, not listed
//                      DIFF-04 excluded        -> added
//                      predicate env = prod    -> predicate changed
//
// The spec reads the diff on the Scopes tab, then approves and activates
// rev B from the same screen.

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const FV = "e2e17950-0000-0000-0000-0000000f0002";
const REQ = {
  "DIFF-01": "e2e17950-0000-0000-0000-00000000b001",
  "DIFF-02": "e2e17950-0000-0000-0000-00000000b002",
  "DIFF-03": "e2e17950-0000-0000-0000-00000000b003",
  "DIFF-04": "e2e17950-0000-0000-0000-00000000b004",
} as const;

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function atlas(path: string, method: string, body?: unknown) {
  const res = await fetch(`${atlasBaseURL()}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${process.env.TEST_BEARER}`,
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
  }
  return res.json();
}

async function createScope(name: string, predicate: unknown): Promise<string> {
  const { framework_scope } = (await atlas("/v1/framework-scopes", "POST", {
    framework_version_id: FV,
    name,
    predicate,
  })) as { framework_scope: { id: string } };
  return framework_scope.id;
}

async function decide(
  scopeID: string,
  code: keyof typeof REQ,
  decision: "applicable" | "excluded",
  justification = "",
) {
  await atlas(
    `/v1/framework-scopes/${scopeID}/applicability/${REQ[code]}`,
    "PUT",
    { decision, justification },
  );
}

test.describe.configure({ mode: "serial" });

test.describe("framework scope · review-screen applicability diff (#1795)", () => {
  let revB = "";

  test.beforeAll(async () => {
    seedFromFixture("framework-scope-diff");

    const revA = await createScope("Diff scope rev A (e2e)", { op: "true" });
    await decide(revA, "DIFF-01", "excluded", "No assets of our own");
    await decide(revA, "DIFF-02", "excluded", "No card data stored");
    await decide(revA, "DIFF-03", "applicable");
    await atlas(`/v1/framework-scopes/${revA}/submit`, "PATCH");
    await atlas(`/v1/framework-scopes/${revA}/approve`, "PATCH");
    await atlas(`/v1/framework-scopes/${revA}/activate`, "PATCH", {
      effective_from: new Date(Date.now() - 60_000).toISOString(),
    });

    revB = await createScope("Diff scope rev B (e2e)", {
      op: "eq",
      dim: "env",
      value: "prod",
    });
    await decide(revB, "DIFF-01", "applicable");
    await decide(
      revB,
      "DIFF-02",
      "excluded",
      "Card data is tokenised at the edge",
    );
    await decide(revB, "DIFF-03", "applicable");
    await decide(revB, "DIFF-04", "excluded", "Fully remote company");
    await atlas(`/v1/framework-scopes/${revB}/submit`, "PATCH");
  });

  test("the review screen lists the changed decisions and the predicate", async ({
    authedPage: page,
  }) => {
    await page.goto(`/framework-scopes/${FV}`);
    const diff = page.getByTestId(`review-diff-${revB}`);
    await expect(diff).toBeVisible();
    await expect(diff).toContainText('"Diff scope rev A (e2e)"');

    const rows = diff.locator("[data-testid^='review-diff-row-']");
    await expect(rows).toHaveCount(3);
    await expect(diff.getByTestId("review-diff-row-DIFF-01")).toHaveAttribute(
      "data-change",
      "decision_changed",
    );
    await expect(diff.getByTestId("review-diff-row-DIFF-02")).toHaveAttribute(
      "data-change",
      "justification_changed",
    );
    await expect(diff.getByTestId("review-diff-row-DIFF-02")).toContainText(
      "Card data is tokenised at the edge",
    );
    await expect(diff.getByTestId("review-diff-row-DIFF-04")).toHaveAttribute(
      "data-change",
      "added",
    );
    await expect(diff.getByTestId("review-diff-row-DIFF-03")).toHaveCount(0);

    await expect(diff.getByTestId("review-diff-count-added")).toHaveText(
      "Added: 1",
    );
    await expect(diff.getByTestId("review-diff-predicate")).toContainText(
      '"prod"',
    );
  });

  test("approving and activating the revision in review makes it current", async ({
    authedPage: page,
  }) => {
    await page.goto(`/framework-scopes/${FV}`);
    await expect(page.getByTestId(`review-diff-${revB}`)).toBeVisible();

    await page.getByRole("button", { name: "Approve", exact: true }).click();
    await page.getByRole("button", { name: "Activate", exact: true }).click();
    await page.getByRole("button", { name: "Confirm", exact: true }).click();

    // Nothing is in review any more, so the diff panel is gone, and rev B
    // is the current scope.
    await expect(page.getByTestId(`review-diff-${revB}`)).toHaveCount(0);
    await expect(
      page.getByRole("row").filter({ hasText: "Diff scope rev B (e2e)" }),
    ).toContainText("Current scope");
    await expect(
      page.getByRole("row").filter({ hasText: "Diff scope rev A (e2e)" }),
    ).toContainText("Superseded");
  });
});
