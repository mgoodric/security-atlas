// #2081 — the sidebar lists every page, and each label is its page's
// title.
//
// Three claims, against the real backend (the nav gates are resolved
// server-side, so a browser route-mock cannot reach them):
//
//   1. Every listed page is reachable by clicking its sidebar entry, and
//      the page it opens carries an H1 equal to the entry's label. That
//      includes the pages that used to be URL-only: Exceptions,
//      Framework scopes, the Implementation checklist, Risk hierarchy.
//   2. With the board-reporting flag off (the demo tenant's seed
//      default), Board packs is not listed; with no NIST CSF framework
//      in the catalog (the CI database loads none), CSF assessment is
//      not listed.
//   3. With the flag on, Board packs is listed and opens. The flag is on
//      only for a dedicated tenant (fixtures/e2e/sidebar-nav.sql), so the
//      flag-off assertions here and in feature-flag-gating.spec.ts keep
//      holding for the demo tenant in parallel workers.
//
// The CSF-loaded case (entry listed, links to the version) is covered at
// the vitest tier (lib/nav.test.ts): loading a global NIST CSF framework
// here would list it for every spec's tenant.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const NAV_TENANT = "00000000-0000-0000-0000-000000002081";

// What the demo tenant's sidebar lists: everything but the flag-gated
// entries and CSF assessment. Admin depends on the caller's `/v1/me`, so
// it is checked only when present.
const DEMO_TENANT_LABELS = [
  "Dashboard",
  "Calendar",
  "Metrics",
  "Activity",
  "Controls",
  "Evidence",
  "Exceptions",
  "Implementation checklist",
  "Risk register",
  "Risk hierarchy",
  "Risk taxonomy",
  "Action plans",
  "Audit periods",
  "Framework scopes",
  "Policies",
  "Vendors",
  "Personnel security",
  "Questionnaires",
  "SCF catalog",
  "Settings",
];

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

// mintJWT mints a bearer for the dedicated tenant through the env-gated
// /v1/test/issue-jwt (slice 201). Throws on anything but a token.
async function mintJWT(roles: string[], superAdmin: boolean): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: NAV_TENANT,
      roles,
      super_admin: superAdmin,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2081: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2081: ${url} returned 200 but no token field`);
  }
  return parsed.token;
}

// signInAs replaces the context's session cookie with `bearer`.
async function signInAs(page: Page, bearer: string): Promise<void> {
  const url = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  await page.context().clearCookies();
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

async function sidebarLabels(page: Page): Promise<string[]> {
  const sidebar = page.getByTestId("sidebar-desktop");
  await expect(sidebar).toBeVisible();
  // The label is the row's first span; the count badge is a sibling.
  return sidebar.locator("a > span:first-child").allInnerTexts();
}

// openFromSidebar clicks the entry labelled `label` and checks the page
// it opens is titled `label`.
async function openFromSidebar(page: Page, label: string): Promise<void> {
  // By the label text, not the link's accessible name: the Controls and
  // Risks rows append a count badge to their name.
  const link = page
    .getByTestId("sidebar-desktop")
    .getByRole("link")
    .filter({ has: page.getByText(label, { exact: true }) });
  const href = await link.getAttribute("href");
  expect(href, `${label}: sidebar entry has an href`).toBeTruthy();
  await link.click();
  await page.waitForURL((u) => u.pathname === href);
  await expect(
    page.locator("main").getByRole("heading", { level: 1 }),
    `${label}: the page it opens is titled "${label}"`,
  ).toHaveText(label);
}

test.describe("#2081 — sidebar lists every page, labels match titles", () => {
  test("every listed page opens from the sidebar under its own title", async ({
    authedPage: page,
  }) => {
    test.setTimeout(180_000);
    await page.goto("/dashboard");
    const labels = await sidebarLabels(page);
    for (const want of DEMO_TENANT_LABELS) {
      expect(labels, `sidebar lists ${want}`).toContain(want);
    }
    for (const label of labels) {
      await openFromSidebar(page, label);
    }
  });

  test("flag off: no Board packs; no CSF framework: no CSF assessment", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");
    const labels = await sidebarLabels(page);
    expect(labels).not.toContain("Board packs");
    expect(labels).not.toContain("Vendor claims");
    expect(labels).not.toContain("CSF assessment");
  });
});

test.describe("#2081 — sidebar with the gated modules on", () => {
  test.beforeAll(() => {
    seedFromFixture("sidebar-nav");
  });

  test("flag on: Board packs and Vendor claims are listed and open", async ({
    authedPage: page,
  }) => {
    await signInAs(page, await mintJWT(["admin"], true));
    await page.goto("/dashboard");
    const labels = await sidebarLabels(page);
    expect(labels).toContain("Board packs");
    expect(labels).toContain("Vendor claims");
    await openFromSidebar(page, "Board packs");
    await openFromSidebar(page, "Vendor claims");
  });

  // Restructuring must not show a role anything it was hidden before: a
  // viewer and an auditor still get no Admin entry.
  for (const role of ["viewer", "auditor"]) {
    test(`${role}: no Admin entry`, async ({ authedPage: page }) => {
      await signInAs(page, await mintJWT([role], false));
      await page.goto("/dashboard");
      const labels = await sidebarLabels(page);
      expect(labels).toContain("Dashboard");
      expect(labels).not.toContain("Admin");
    });
  }
});
