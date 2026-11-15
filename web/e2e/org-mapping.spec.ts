// #1800 — overlay edges shown as "org mapping" in the web UI, against the
// REAL stack (no route mock): the BFF, the platform's traversal reads, and
// PostgreSQL RLS on fw_to_scf_overlay_edges.
//
// Seeded by fixtures/e2e/org-mapping.sql: requirement OM-01 maps to anchor
// OM-E2E-A1 through a catalog edge, and to OM-E2E-A2 through the demo
// tenant's overlay edge. The demo tenant's control C2 sits on A2, so it is
// credited for OM-01 only through the overlay. A second tenant (B) has no
// overlay edge.
//
//   1. requirement detail, demo tenant: an "org mapping" entry with its
//      rationale, source, author and date, apart from the catalog mapping;
//   2. control coverage, demo tenant: the OM-01 row is marked as credited
//      through the org mapping;
//   3. tenant B: the requirement shows its catalog mapping only, and the
//      anchor detail for A2 does not list OM-01.
//
// Tenant B's credential is minted through /v1/test/issue-jwt (ATLAS_TEST_MODE),
// as tenant-switch-rls.spec.ts does, with super_admin=true for the same
// reason (its decisions log D4): super_admin clears OPA authz for the
// synthetic user and does NOT bypass RLS, so RLS stays the only gate on
// which overlay edges the tenant sees. The token is fetched at runtime and
// never written as a literal here.

import { expect, test, type Page } from "./fixtures";
import { seedFromFixture } from "./seed";

const REQUIREMENT_ID = "e2e18000-0000-0000-0000-00000000b001";
const OVERLAY_ANCHOR_ID = "e2e18000-0000-0000-0000-00000000a002";
const CONTROL_ID = "e2e18000-0000-0000-0000-00000000c002";
const TENANT_B = "e2e18000-0000-0000-0000-00000000bbbb";

const RATIONALE = "Auditor tested the NDA control under OM-01";
const SOURCE = "FY25 SOC 2 report, section 4, OM-01";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintTenantBJWT(): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: TENANT_B,
      available_tenants: [TENANT_B],
      roles: ["admin"],
      super_admin: true,
    }),
  });
  if (!res.ok) {
    throw new Error(`${url} returned ${res.status}: ${await res.text()}`);
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`${url} returned 200 but no token field`);
  }
  return parsed.token;
}

async function signInAs(page: Page, bearer: string): Promise<void> {
  const url = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  await page.context().addCookies([
    {
      name: "atlas_jwt",
      value: bearer,
      domain: url.hostname,
      path: "/",
      httpOnly: true,
      secure: url.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

// gotoRequirement opens the requirement detail page and waits for its BFF
// read, so assertions on the rendered mappings do not race the fetch.
async function gotoRequirement(page: Page): Promise<void> {
  const resp = page.waitForResponse(
    (r) =>
      r.url().includes(`/api/requirements/${REQUIREMENT_ID}/coverage`) &&
      r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto(`/requirements/${REQUIREMENT_ID}`);
  await resp;
}

test.describe("overlay edges shown as org mapping (#1800)", () => {
  test.beforeAll(() => {
    seedFromFixture("org-mapping");
  });

  test("a requirement with an overlay edge shows an org mapping entry with its rationale and source", async ({
    authedPage: page,
  }) => {
    await gotoRequirement(page);

    const org = page.getByTestId("requirement-org-mappings");
    await expect(org).toBeVisible();
    const row = org.getByTestId("requirement-anchor-row");
    await expect(row).toHaveCount(1);
    await expect(row).toHaveAttribute("data-origin", "overlay");
    await expect(row).toContainText("OM-E2E-A2");
    // The badge is the `org-mapping` <Term>: its button's accessible name is
    // the label. The badge's own text also holds the tooltip definition,
    // which <Term> keeps in the DOM.
    await expect(
      row
        .getByTestId("org-mapping-badge")
        .getByRole("button", { name: "org mapping", exact: true }),
    ).toBeVisible();
    await expect(row.getByTestId("org-mapping-rationale")).toHaveText(
      RATIONALE,
    );
    await expect(row.getByTestId("org-mapping-source")).toHaveText(SOURCE);
    await expect(row.getByTestId("org-mapping-added")).toHaveText(
      "2026-11-01 by org-mapping-e2e-author",
    );

    // The catalog edge is listed apart, unlabelled.
    const catalog = page.getByTestId("requirement-catalog-mappings");
    const catalogRow = catalog.getByTestId("requirement-anchor-row");
    await expect(catalogRow).toHaveCount(1);
    await expect(catalogRow).toHaveAttribute("data-origin", "catalog");
    await expect(catalogRow).toContainText("OM-E2E-A1");
    await expect(catalog.getByTestId("org-mapping-badge")).toHaveCount(0);

    // The control behind the overlay anchor is credited only through it.
    const control = page.getByTestId("requirement-control");
    await expect(control).toHaveAttribute("data-org-only", "true");
    await expect(control.getByTestId("org-mapping-badge")).toBeVisible();

    // The anchor detail lists the requirement as an org mapping too.
    await page.goto(`/catalog/scf/${OVERLAY_ANCHOR_ID}`);
    const anchorRow = page.getByTestId("anchor-requirement-row");
    await expect(anchorRow).toHaveCount(1);
    await expect(anchorRow).toHaveAttribute("data-origin", "overlay");
    await expect(anchorRow.getByTestId("org-mapping-source")).toHaveText(
      SOURCE,
    );
  });

  test("control coverage marks the criterion credited through the overlay", async ({
    authedPage: page,
  }) => {
    await page.goto(`/controls/${CONTROL_ID}`);

    const table = page.getByTestId("coverage-table").first();
    await expect(table).toBeVisible({ timeout: 30_000 });
    const row = table.getByTestId("coverage-row").filter({ hasText: "OM-01" });
    await expect(row).toHaveCount(1);
    await expect(row).toHaveAttribute("data-origin", "overlay");
    const mark = row.getByTestId("coverage-org-mapping");
    await expect(mark).toContainText(
      "Credited through your org mapping, not the catalog crosswalk",
    );
    await expect(mark.getByTestId("org-mapping-rationale")).toHaveText(
      RATIONALE,
    );
    await expect(mark.getByTestId("org-mapping-source")).toHaveText(SOURCE);
  });

  test("a second tenant's user doesn't see it", async ({ page }) => {
    await signInAs(page, await mintTenantBJWT());

    await gotoRequirement(page);
    // The catalog edge is global: tenant B sees it.
    const catalogRow = page
      .getByTestId("requirement-catalog-mappings")
      .getByTestId("requirement-anchor-row");
    await expect(catalogRow).toHaveCount(1);
    await expect(catalogRow).toContainText("OM-E2E-A1");
    // The demo tenant's overlay edge is not.
    await expect(page.getByTestId("requirement-org-mappings")).toHaveCount(0);
    await expect(page.getByTestId("org-mapping-badge")).toHaveCount(0);
    await expect(page.getByText(RATIONALE)).toHaveCount(0);
    await expect(page.getByText(SOURCE)).toHaveCount(0);

    // Nor does the overlay anchor list the requirement for tenant B.
    const anchorResp = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/anchors/${OVERLAY_ANCHOR_ID}/requirements`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto(`/catalog/scf/${OVERLAY_ANCHOR_ID}`);
    await anchorResp;
    await expect(
      page.getByText("No requirement mappings", { exact: true }),
    ).toBeVisible();
    await expect(page.getByTestId("anchor-requirement-row")).toHaveCount(0);
  });
});
