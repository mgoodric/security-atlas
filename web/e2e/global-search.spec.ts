// Slice 272 / OE-398c — Playwright e2e for the global ⌘K search flow,
// end to end, against the REAL search endpoint.
//
// What this spec adds over `controls-top-bar.spec.ts` (slice 223/361/398):
// that spec `page.route`s `/api/search` and fulfills a hand-written hits
// payload, so it pins the surface's INTERACTION contract (shortcut
// focuses, popover groups, Enter and click both navigate, ARIA wiring)
// while the endpoint behind it stays stubbed. Every leg of the flow is
// therefore proven except the one the operator actually depends on: that
// what they type reaches a real query and what comes back is a real
// record they can open.
//
// This spec closes that leg. It installs NO route mock anywhere. The
// keystrokes drive:
//
//   web/components/shell/global-search.tsx  (the shipped ⌘K surface)
//     → BFF  web/app/api/search/route.ts    (bearer-forwarding proxy)
//       → upstream GET /v1/search           (internal/api/search)
//         → Postgres, RLS-scoped by the tenant GUC
//
// and then follow the returned hit's own href to its detail page. The
// corpus is seeded by `fixtures/e2e/global-search.sql`: one control, one
// risk, one evidence record, each carrying the token `AcmeVault`, which
// appears in no other fixture and in no bundled SCF anchor.
//
// Why the result ORDER below is deterministic (and not a guess): the
// endpoint sorts relevance DESC, then type ASC, then id ASC. A
// single-token query scores every matching row 1.0, so the merge is
// ordered by type name — and the UI's flat keyboard-navigation order is
// GROUP_ORDER (anchors, controls, risks, evidence) flattened. With zero
// `anchors` hits the first flat row is the control and the second is the
// risk. The spec asserts the absent anchors group explicitly so a future
// catalog change that starts matching `AcmeVault` fails loudly on that
// assertion instead of silently changing which row Enter opens.
//
// Timeouts follow the slice-275 convention documented in
// `web/e2e/README.md`: gate the first visibility assertion on the
// network round-trip that drives it, and carry a 30s backstop for CI
// load. `waitForResponse` is always set up BEFORE the action that
// triggers the request (a Playwright invariant).
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/global-search.spec.ts

import { expect, test } from "./fixtures";

import { seedFromFixture } from "./seed";

// Fixture-local rows. Mirrors fixtures/e2e/global-search.sql — spec-local
// consts rather than fields on the shared `seeded` accessor, matching the
// `tenant-switch-rls.spec.ts` precedent for per-fixture ids.
const CONTROL_ID = "33333333-3333-3333-3333-33333333c001";
const RISK_ID = "77777777-7777-7777-7777-77777777c001";
const EVIDENCE_ID = "66666666-6666-6666-6666-66666666c001";

const CONTROL_TITLE = "AcmeVault secret rotation — production credentials";
const RISK_TITLE = "AcmeVault credential sprawl";
// The endpoint synthesizes the evidence label as
// "<evidence_kind> · <control_ref>" (internal/api/search/search.go).
const EVIDENCE_TITLE = "acmevault.secret_rotation.v1 · IAC-10";

// The query the operator types. Lower-case on purpose — the match is
// ILIKE, so this also pins case-insensitivity through the whole path.
const QUERY = "acmevault";

interface SearchHitWire {
  id: string;
  type: string;
  title: string;
  snippet: string;
  relevance_score: number;
}

interface SearchResponseWire {
  hits: SearchHitWire[];
  count: number;
  partial_types: string[];
}

test.describe("global ⌘K search — real endpoint (OE-398c)", () => {
  test.beforeAll(() => {
    seedFromFixture("global-search");
  });

  test("⌘K opens the search surface, a typed query returns real controls / risks / evidence hits, and Enter navigates to the control", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");

    const input = page.getByTestId("global-search-input");
    await expect(input).toBeVisible({ timeout: 30_000 });
    // Baseline: the surface is closed and unfocused before the shortcut.
    await expect(input).not.toBeFocused();
    await expect(input).toHaveAttribute("aria-expanded", "false");

    // Set up the real round-trip BEFORE the keystrokes that trigger it.
    const searchResponse = page.waitForResponse(
      (r) => r.url().includes("/api/search") && r.status() === 200,
      { timeout: 30_000 },
    );

    // 1. Open with the keyboard shortcut.
    await page.keyboard.press("Meta+K");
    await expect(input).toBeFocused();

    // 2. Type the query — real keystrokes into the focused input, so the
    //    250ms debounce and the fetch it gates are exercised as an
    //    operator would.
    await page.keyboard.type(QUERY);

    // 3. The response is the real endpoint's, not a fulfilled mock. Its
    //    body carries the three seeded rows under their own types, which
    //    is the "not a mock UI" proof: these ids exist only because the
    //    fixture INSERTed them and RLS admitted them for this tenant.
    const body = (await (await searchResponse).json()) as SearchResponseWire;
    const seededByType = new Map(
      body.hits
        .filter((h) => [CONTROL_ID, RISK_ID, EVIDENCE_ID].includes(h.id))
        .map((h) => [h.type, h.id]),
    );
    expect(seededByType.get("controls")).toBe(CONTROL_ID);
    expect(seededByType.get("risks")).toBe(RISK_ID);
    expect(seededByType.get("evidence")).toBe(EVIDENCE_ID);
    // No type was dropped by the per-type OPA admit for this credential;
    // otherwise a missing group below would be ambiguous between "the
    // query found nothing" and "the role hid it".
    expect(body.partial_types).toEqual([]);

    // 4. The popover renders the hits, grouped across all three types.
    const popover = page.getByTestId("global-search-popover");
    await expect(popover).toBeVisible({ timeout: 30_000 });
    await expect(input).toHaveAttribute("aria-expanded", "true");
    await expect(
      page.getByTestId("global-search-group-controls"),
    ).toBeVisible();
    await expect(page.getByTestId("global-search-group-risks")).toBeVisible();
    await expect(
      page.getByTestId("global-search-group-evidence"),
    ).toBeVisible();
    // The SCF-anchor catalog does not carry the token; the absent group
    // is what makes the control the first flat row (see the header).
    await expect(page.getByTestId("global-search-group-anchors")).toHaveCount(
      0,
    );

    await expect(
      page.getByTestId("global-search-row-controls").first(),
    ).toContainText(CONTROL_TITLE);
    await expect(
      page.getByTestId("global-search-row-risks").first(),
    ).toContainText(RISK_TITLE);
    await expect(
      page.getByTestId("global-search-row-evidence").first(),
    ).toContainText(EVIDENCE_TITLE);

    // The control row is the active option — Enter opens it.
    await expect(input).toHaveAttribute(
      "aria-activedescendant",
      `global-search-option-controls-${CONTROL_ID}`,
    );

    // 5. Select it and land on the real record. The coverage call is the
    //    detail page's mount-path query; gating on it closes the race
    //    between navigation and the title render (README slice-275).
    const coverageResponse = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/controls/${CONTROL_ID}/coverage`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(new RegExp(`/controls/${CONTROL_ID}$`), {
      timeout: 30_000,
    });
    await coverageResponse;

    // The destination is the seeded control itself, not a 404 empty-state
    // and not a generic error boundary.
    await expect(page.getByTestId("control-title")).toHaveText(CONTROL_TITLE, {
      timeout: 30_000,
    });
    await expect(page.getByTestId("control-detail-empty")).toHaveCount(0);

    // The surface closes behind the operator and clears its query, so the
    // detail page is not covered by a stale result list.
    await expect(popover).not.toBeVisible();
    await expect(input).toHaveValue("");
  });

  test("arrow-keys move the selection across result types and Enter navigates to the risk hit", async ({
    authedPage: page,
  }) => {
    // The first test proves the first flat row navigates. This one proves
    // the SELECTION is real: a different type, reached by ArrowDown,
    // routes to that type's own destination (risks have no per-row detail
    // page, so hrefForHit sends them to the hierarchy deep link).
    await page.goto("/controls");

    const input = page.getByTestId("global-search-input");
    await expect(input).toBeVisible({ timeout: 30_000 });

    const searchResponse = page.waitForResponse(
      (r) => r.url().includes("/api/search") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.keyboard.press("Meta+K");
    await expect(input).toBeFocused();
    await page.keyboard.type(QUERY);
    await searchResponse;

    await expect(page.getByTestId("global-search-popover")).toBeVisible({
      timeout: 30_000,
    });
    await expect(input).toHaveAttribute(
      "aria-activedescendant",
      `global-search-option-controls-${CONTROL_ID}`,
    );

    await page.keyboard.press("ArrowDown");
    await expect(input).toHaveAttribute(
      "aria-activedescendant",
      `global-search-option-risks-${RISK_ID}`,
    );

    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(
      new RegExp(`/risks/hierarchy\\?focus=${RISK_ID}$`),
      { timeout: 30_000 },
    );
  });
});
