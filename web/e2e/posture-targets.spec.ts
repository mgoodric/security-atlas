// #2136 — posture targets on the dashboard's framework-posture tiles.
//
// Seeded by fixtures/e2e/posture-targets.sql: version `2136-scoped-e2e`
// (two requirements, no crosswalk, so coverage 0.0%) with an activated
// scope and no target, and version `2136-unscoped-e2e` with no scope.
//
// The tests run in order against that one target (serial mode, so a retry
// re-runs the group from a fresh seed):
//   1. with no target the tile is neutral, says "No target set" and offers
//      "Set target" (the e2e user is an admin); setting 90 / 80 / 60
//      (higher is better) turns the 0.0% coverage red, "Critical";
//      editing it to 0 / 0 / 0 turns it green, "On target"; the open
//      dialog passes the serious/critical axe gate;
//   2. the unscoped version still says "Scope not set", with no
//      percentage and no "Set target".

import { describeAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const SCOPED = "2136-scoped-e2e";
const UNSCOPED = "2136-unscoped-e2e";

test.describe.configure({ mode: "serial" });

test.describe("posture targets (#2136)", () => {
  test.beforeAll(() => {
    seedFromFixture("posture-targets");
  });

  test("no target is neutral with Set target; a target colours the tile by threshold", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");
    const tile = page.getByTestId("framework-tile").filter({ hasText: SCOPED });
    const coverage = tile.getByTestId("framework-tile-coverage");

    // No target: neutral, no traffic-light classes, "No target set".
    await expect(coverage).toHaveText("0.0%");
    await expect(coverage).toHaveAttribute("data-tone", "neutral");
    await expect(coverage).not.toHaveClass(/destructive|emerald|amber/);
    await expect(tile.getByTestId("framework-tile-no-target")).toHaveText(
      "No target set",
    );
    const setTarget = tile.getByTestId("framework-tile-set-target");
    await expect(setTarget).toHaveText("Set target");

    // Set 90 / 80 / 60, higher is better: 0.0% is at or below critical.
    await setTarget.click();
    const dialog = page.getByTestId("posture-target-dialog");
    await expect(dialog).toBeVisible();
    const findings = await seriousAxeViolations(page);
    expect(findings, describeAxe(findings)).toEqual([]);
    await dialog.getByLabel("Direction").selectOption("higher_is_better");
    await dialog.getByLabel("Target coverage (%)").fill("90");
    await dialog.getByLabel("Warning threshold (%)").fill("80");
    await dialog.getByLabel("Critical threshold (%)").fill("60");
    await dialog.getByRole("button", { name: "Save target" }).click();
    await expect(dialog).toBeHidden();

    await expect(coverage).toHaveAttribute("data-tone", "red");
    await expect(coverage).toHaveClass(/destructive/);
    await expect(tile.getByTestId("framework-tile-target")).toHaveText(
      "Critical · target 90%",
    );
    await expect(tile.getByTestId("framework-tile-no-target")).toHaveCount(0);

    // The order is checked before saving: a warning above the target is
    // refused in the dialog.
    await tile.getByTestId("framework-tile-set-target").click();
    await expect(dialog).toBeVisible();
    await dialog.getByLabel("Warning threshold (%)").fill("95");
    await expect(dialog.getByTestId("posture-target-problem")).toContainText(
      "critical ≤ warning ≤ target",
    );
    await expect(
      dialog.getByRole("button", { name: "Save target" }),
    ).toBeDisabled();

    // Edit to 0 / 0 / 0: 0.0% meets the target.
    await dialog.getByLabel("Target coverage (%)").fill("0");
    await dialog.getByLabel("Warning threshold (%)").fill("0");
    await dialog.getByLabel("Critical threshold (%)").fill("0");
    await dialog.getByRole("button", { name: "Save target" }).click();
    await expect(dialog).toBeHidden();

    await expect(coverage).toHaveAttribute("data-tone", "green");
    await expect(coverage).toHaveClass(/emerald/);
    await expect(tile.getByTestId("framework-tile-target")).toHaveText(
      "On target · target 0%",
    );
    await expect(tile.getByTestId("framework-tile-set-target")).toHaveText(
      "Edit target",
    );
  });

  test("an unscoped framework still shows Scope not set", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");
    const tile = page
      .getByTestId("framework-tile")
      .filter({ hasText: UNSCOPED });
    await expect(tile.getByTestId("framework-tile-scope-not-set")).toHaveText(
      "Scope not set",
    );
    await expect(tile.getByTestId("framework-tile-coverage")).toHaveCount(0);
    await expect(tile.getByTestId("framework-tile-set-target")).toHaveCount(0);
  });
});
