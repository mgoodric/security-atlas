// Slice 660 + #2092 — Playwright e2e for feature-flag nav + route gating.
//
// Both gating flags (`board.reporting`, `oscal.export`) ship OFF. The
// demo tenant has them turned ON by global-setup so the module specs can
// render the pages; this spec signs in to a SEPARATE tenant that nobody
// toggles, so both flags stay at the seed default (off) and the spec does
// not race the module specs over a shared flag row.
//
// Everything here runs against the real backend: since #2092 the flag is
// read server-side (FeatureGate in the route layout, and the sidebar's
// enabled-modules read), in Node, where `page.route` cannot reach.
//
// Three surfaces:
//
//   1. NAV OMISSION. The sidebar omits Vendor Claims + Board Packs.
//   2. OFF STATE, BY AUDIENCE. Direct navigation to a gated route shows a
//      calm off state: an admin (`is_admin`) gets the Admin › Features
//      link, a viewer is told to ask an administrator and gets no link.
//   3. NO FLASH. The off state is in the server-rendered HTML, and the
//      module's own UI (the generate form, the loading skeletons, the
//      "Could not load" error) never enters the DOM.

import { expect, test, type Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";

// A tenant with no feature_flags rows: every flag reads as its seed
// default. "20f0" ties the id to #2092 (FeatureGate off state).
const FLAGS_OFF_TENANT = "00000000-0000-0000-0000-0000000020f0";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

type Audience = "admin" | "viewer";

// mintJWT asks the ATLAS_TEST_MODE-gated issuer for a token in the
// flags-off tenant. The admin is a super-admin (`is_admin` on /v1/me);
// the viewer is not.
async function mintJWT(audience: Audience): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(
      audience === "admin"
        ? { tenant_id: FLAGS_OFF_TENANT, roles: ["admin"], super_admin: true }
        : {
            tenant_id: FLAGS_OFF_TENANT,
            roles: ["viewer"],
            super_admin: false,
          },
    ),
  });
  if (!res.ok) {
    throw new Error(
      `#2092: ${url} returned ${
        res.status
      }: ${await res.text()} (is ATLAS_TEST_MODE=1 set on the atlas server?)`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2092: ${url} returned 200 but no token field`);
  }
  return parsed.token;
}

async function signIn(
  page: Page,
  baseURL: string | undefined,
  audience: Audience,
): Promise<void> {
  const url = new URL(baseURL ?? "http://localhost:3000");
  await page.context().addCookies([
    {
      name: ATLAS_JWT_COOKIE,
      value: await mintJWT(audience),
      domain: url.hostname,
      path: "/",
      httpOnly: true,
      secure: url.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

// watchForFlash records, from before the first byte is parsed, whether
// any of the given testids ever enters the DOM. A client-side gate would
// mount the module (form, skeletons) and swap it out; a server-side one
// never sends it.
async function watchForFlash(page: Page, testids: string[]): Promise<void> {
  await page.addInitScript((ids: string[]) => {
    const seen: string[] = [];
    (window as unknown as { __flashSeen: string[] }).__flashSeen = seen;
    const check = () => {
      for (const id of ids) {
        if (
          !seen.includes(id) &&
          document.querySelector(`[data-testid="${id}"]`)
        ) {
          seen.push(id);
        }
      }
    };
    new MutationObserver(check).observe(document, {
      childList: true,
      subtree: true,
    });
  }, testids);
}

async function flashSeen(page: Page): Promise<string[]> {
  return page.evaluate(
    () => (window as unknown as { __flashSeen: string[] }).__flashSeen,
  );
}

const BOARD_PACK_UI = [
  "board-packs-page",
  "board-pack-generate-form",
  "board-pack-period-end",
];
const VENDOR_CLAIMS_UI = [
  "component-definitions-page",
  "component-definitions-loading",
  "component-definitions-error",
];

test.describe("slice 660 — feature-flag nav gating", () => {
  test("a flags-off tenant's sidebar omits Vendor Claims + Board Packs", async ({
    page,
    baseURL,
  }) => {
    await signIn(page, baseURL, "admin");
    await page.goto("/dashboard");
    const sidebar = page.getByTestId("sidebar-desktop");
    await expect(sidebar).toBeVisible();
    await expect(
      sidebar.getByRole("link", { name: "Board Packs" }),
    ).toHaveCount(0);
    await expect(
      sidebar.getByRole("link", { name: "Vendor Claims" }),
    ).toHaveCount(0);
    // A non-gated entry still renders (sanity: the nav itself works).
    await expect(
      sidebar.getByRole("link", { name: "Dashboard" }),
    ).toBeVisible();
  });
});

test.describe("#2092 — FeatureGate off state", () => {
  test("Board packs, flag off, admin: the Admin › Features link and no form flash", async ({
    page,
    baseURL,
  }) => {
    await signIn(page, baseURL, "admin");

    // The off state is in the server's HTML; the form is not.
    const html = await (await page.request.get("/board-packs")).text();
    expect(html).toContain('data-testid="feature-off-state"');
    expect(html).not.toContain('data-testid="board-pack-generate-form"');

    await watchForFlash(page, BOARD_PACK_UI);
    await page.goto("/board-packs");
    const panel = page.getByTestId("feature-off-state");
    await expect(panel).toBeVisible();
    await expect(panel).toHaveAttribute("data-audience", "off-admin");
    await expect(panel).toContainText("This feature is off.");
    const link = panel.getByTestId("feature-off-admin-link");
    await expect(link).toHaveText("Enable in Admin › Features");
    await expect(link).toHaveAttribute("href", "/admin/features");
    expect(await flashSeen(page)).toEqual([]);
  });

  test("Board packs, flag off, viewer: ask an administrator, no Admin link", async ({
    page,
    baseURL,
  }) => {
    await signIn(page, baseURL, "viewer");

    const html = await (await page.request.get("/board-packs")).text();
    expect(html).toContain('data-testid="feature-off-state"');
    expect(html).not.toContain('data-testid="board-pack-generate-form"');
    expect(html).not.toContain("/admin/features");

    await watchForFlash(page, BOARD_PACK_UI);
    await page.goto("/board-packs");
    const panel = page.getByTestId("feature-off-state");
    await expect(panel).toBeVisible();
    await expect(panel).toHaveAttribute("data-audience", "off-member");
    await expect(panel).toContainText(
      "This feature isn't turned on for your organisation. Ask an administrator.",
    );
    await expect(page.getByTestId("feature-off-admin-link")).toHaveCount(0);
    await expect(page.locator('a[href="/admin/features"]')).toHaveCount(0);
    expect(await flashSeen(page)).toEqual([]);
  });

  test("Vendor claims, flag off: the off state, not an error", async ({
    page,
    baseURL,
  }) => {
    await signIn(page, baseURL, "admin");
    await watchForFlash(page, VENDOR_CLAIMS_UI);
    await page.goto("/oscal/component-definitions");
    const panel = page.getByTestId("feature-off-state");
    await expect(panel).toBeVisible();
    await expect(panel).toContainText("This feature is off.");
    await expect(page.getByText("Could not load")).toHaveCount(0);
    await expect(page.getByText("feature disabled")).toHaveCount(0);
    expect(await flashSeen(page)).toEqual([]);
  });
});
