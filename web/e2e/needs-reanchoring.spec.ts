// Issue #2176 — dashboard warning for controls that need SCF re-anchoring.
//
// The banner reads GET /api/controls/needs-reanchoring (a client read; the
// dashboard's server prefetch does not cover it). These specs shape that
// BFF with page.route:
//
//   * flagged controls -> the banner shows the count and links to
//     /controls/needs-reanchoring, which lists each control's remap note;
//   * an empty list (every control re-anchored) -> no banner, and the list
//     page says nothing needs re-anchoring;
//   * a 403 (a role that cannot read controls) -> no banner, the same role
//     filter as the control list;
//   * any other failure (500) -> an inline error on the dashboard and the
//     list page, never an empty state (#2222).
//
// The Postgres-backed path (RLS, pinned and rebindable controls excluded,
// re-uploading on a current anchor clears the row) is covered by
// internal/api/controls/reanchoring_integration_test.go.

import type { Page } from "@playwright/test";

import type { NeedsReanchoringResponse } from "../lib/api/reanchoring";

import { expect, test } from "./fixtures";

const ROUTE = "**/api/controls/needs-reanchoring";

const empty: NeedsReanchoringResponse = { controls: [], count: 0 };

const flagged: NeedsReanchoringResponse = {
  controls: [
    {
      id: "00000000-0000-4000-8000-000000002176",
      bundle_id: "tenant-iac-21",
      title: "Privileged access is managed",
      legacy_scf_id: "IAC-21",
      legacy_anchor_title: "Privileged Account Management",
      legacy_release: "test-2026.1",
      current_release: "test-2026.3",
      note: "IAC-21 -> IAC-10 in test-2026.3 (concept)",
    },
    {
      id: "00000000-0000-4000-8000-000000002177",
      bundle_id: "tenant-iac-10",
      title: "Authenticators are managed",
      legacy_scf_id: "IAC-10",
      legacy_anchor_title: "Authenticator Management",
      legacy_release: "test-2026.1",
      current_release: "test-2026.3",
      note: "IAC-10 -> IAC-14 in test-2026.3 (legacy)",
    },
  ],
  count: 2,
};

async function mockList(page: Page, status: number, body: unknown) {
  await page.route(ROUTE, async (route) => {
    await route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
}

test.describe("SCF re-anchoring warning", () => {
  test("flagged controls: banner with count links to the list with remap notes", async ({
    authedPage: page,
  }) => {
    await mockList(page, 200, flagged);
    await page.goto("/dashboard");

    const banner = page.getByTestId("reanchoring-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "status");
    await expect(page.getByTestId("reanchoring-banner-title")).toHaveText(
      "2 controls need re-anchoring",
    );
    await expect(banner).toContainText("SCF test-2026.3");

    const link = page.getByTestId("reanchoring-banner-link");
    await expect(link).toHaveAttribute("href", "/controls/needs-reanchoring");
    await link.click();

    await expect(page).toHaveURL(/\/controls\/needs-reanchoring$/);
    await expect(page.getByTestId("needs-reanchoring-page")).toBeVisible();
    await expect(page.getByTestId("needs-reanchoring-row")).toHaveCount(2);
    const notes = page.getByTestId("needs-reanchoring-note");
    await expect(notes.nth(0)).toHaveText(
      "IAC-21 -> IAC-10 in test-2026.3 (concept)",
    );
    await expect(notes.nth(1)).toHaveText(
      "IAC-10 -> IAC-14 in test-2026.3 (legacy)",
    );
    await expect(
      page.getByRole("link", { name: "Privileged access is managed" }),
    ).toHaveAttribute("href", "/controls/00000000-0000-4000-8000-000000002176");
  });

  test("nothing left to re-anchor: no banner, and the list says so", async ({
    authedPage: page,
  }) => {
    await mockList(page, 200, empty);
    const read = page.waitForResponse(
      (r) => r.url().includes("/api/controls/needs-reanchoring") && r.ok(),
    );
    await page.goto("/dashboard");
    await read;
    await expect(page.getByTestId("program-dashboard")).toBeVisible();
    await expect(page.getByTestId("reanchoring-banner")).toHaveCount(0);

    await page.goto("/controls/needs-reanchoring");
    await expect(page.getByTestId("needs-reanchoring-empty")).toBeVisible();
  });

  test("a role that cannot read controls (403): no banner", async ({
    authedPage: page,
  }) => {
    await mockList(page, 403, { error: "forbidden" });
    const read = page.waitForResponse((r) =>
      r.url().includes("/api/controls/needs-reanchoring"),
    );
    await page.goto("/dashboard");
    expect((await read).status()).toBe(403);
    await expect(page.getByTestId("program-dashboard")).toBeVisible();
    await expect(page.getByTestId("reanchoring-banner")).toHaveCount(0);
    await expect(page.getByTestId("reanchoring-banner-error")).toHaveCount(0);
  });

  test("a failed read (500) shows an inline error, not an empty state", async ({
    authedPage: page,
  }) => {
    await mockList(page, 500, { error: "upstream error" });
    await page.goto("/dashboard");
    await expect(page.getByTestId("reanchoring-banner-error")).toBeVisible();
    await expect(page.getByTestId("reanchoring-banner-error")).toContainText(
      "upstream error",
    );
    await expect(page.getByTestId("reanchoring-banner")).toHaveCount(0);

    await page.goto("/controls/needs-reanchoring");
    await expect(page.getByTestId("needs-reanchoring-error")).toBeVisible();
    await expect(page.getByTestId("needs-reanchoring-empty")).toHaveCount(0);
  });
});
