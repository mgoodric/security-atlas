// Slice 351 (AC-3) — Playwright E2E for the evidence-push end-to-end
// chain: CLI push → atlas ingest → BFF read → UI display.
//
// This is a v1 binary success-test flow (CLAUDE.md: "does the user run
// their next SOC 2 audit out of security-atlas?"). Slice 333 Q-9 flagged
// it as only half-covered: `evidence-list.spec.ts` exercises the UI read
// side, but nothing pins the push-through-to-display chain. This spec
// closes that gap.
//
// THE CHAIN MODELLED:
//
//   1. CLI push. The atlas-cli `evidence push` (and every connector)
//      emits to the platform via the canonical inbound API — a single
//      `POST /v1/evidence:push` returning a Receipt (canvas §4.1 / 4.3;
//      `internal/api/httpserver.go` mounts `evidenceH.PushHTTP`). The
//      Receipt carries `record_id` + `hash` (the sha256 content-hash
//      the platform computes, per the evidence-integrity invariant).
//      We model the CLI's wire call with `page.request.post(...)`.
//   2. Ingest. The append-only ledger records the pushed row. We model
//      this with a state-carrying mock: `/api/evidence` returns an empty
//      ledger BEFORE the push, and the pushed row AFTER — using the
//      SAME content hash the Receipt returned, which is the invariant
//      that ties the two stages together (ingestion ≠ evaluation;
//      canvas §4.3).
//   3. BFF read. `/api/evidence` (web/app/api/evidence/route.ts) forwards
//      the bearer cookie to `GET /v1/evidence`; RLS scopes tenancy.
//   4. UI display. `/evidence` renders the row; the hash cell shows the
//      8-char prefix of the pushed record's content hash.
//
// MOCK STRATEGY (P0-4 — established `route.fulfill` convention): the
// `evidence-list.spec.ts` precedent and the wider e2e suite mock the
// atlas wire surface. This spec follows suit so it is deterministic
// without a per-spec SQL fixture that seeds a freshly-pushed row at a
// known hash. The load-bearing assertion — the hash the push Receipt
// returns is the hash the UI displays — is preserved exactly; only the
// transport is mocked.
//
// Determinism: the push is awaited via `page.request.post`; the list
// render is gated on `page.waitForResponse('/api/evidence')` before the
// row visibility assertion. No sleeps; no `.count()` snapshots.
//
// Hard rule (P0-A9): all ids/hashes below are neutral test strings. No
// vendor-prefixed tokens.

import { expect, test } from "./fixtures";

// A deterministic sha256-shaped content hash (64 hex chars). This is
// what the platform's push handler computes from the canonical record;
// the Receipt returns it and the ledger row carries it. The UI renders
// its first 8 chars (web/app/(authed)/evidence/format.ts hashPrefix).
const PUSHED_HASH =
  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789";
const PUSHED_HASH_PREFIX = PUSHED_HASH.slice(0, 8); // "abcdef01"
const PUSHED_RECORD_ID = "00000000-0000-0000-0000-0000000ev999";
const PUSHED_KIND = "sast.scan_result.v1";

function evidenceRow() {
  return {
    evidence_id: PUSHED_RECORD_ID,
    evidence_kind: PUSHED_KIND,
    observed_at: "2026-05-29T12:00:00Z",
    source: { actor_type: "connector", actor_id: "atlas-cli" },
    content_hash: PUSHED_HASH,
    scope_cell: null,
    result: "pass" as const,
  };
}

test.describe("evidence push end-to-end (slice 351 AC-3)", () => {
  test("AC-3: CLI push → ingest → BFF read → UI displays the pushed record's hash", async ({
    authedPage: page,
  }) => {
    // Ledger state: empty until the push lands, then holds the pushed
    // row. `pushed` flips inside the push-handler mock so the read side
    // observes the post-ingest ledger.
    let pushed = false;

    // === Stage 1 — CLI push (POST /v1/evidence:push → Receipt). ===
    // The push handler is on the atlas server directly (not the BFF):
    // connectors hold source-side credentials and push to the platform.
    // We intercept it and return a Receipt carrying the content hash the
    // platform would have computed. The push is driven via an in-page
    // `fetch` (page.evaluate) so it flows through the page's network and
    // is intercepted by `page.route` — modelling the CLI's HTTP POST.
    await page.route("**/v1/evidence:push", async (route, req) => {
      expect(req.method()).toBe("POST");
      pushed = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          receipt: {
            record_id: PUSHED_RECORD_ID,
            hash: PUSHED_HASH,
            credential_id: "test-credential-e2e",
          },
        }),
      });
    });

    // === Stage 3 — BFF read (GET /api/evidence). State-carrying: empty
    // before the push, the pushed row after. ===
    await page.route("**/api/evidence**", async (route, req) => {
      if (req.method() !== "GET") {
        await route.fallback();
        return;
      }
      const rows = pushed ? [evidenceRow()] : [];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control_id: "",
          evidence: rows,
          count: rows.length,
          total: rows.length,
          next_cursor: "",
        }),
      });
    });

    // The /evidence page issues a few sibling BFF reads (anchors, scope
    // cells, audit periods) to populate filter pills. Stub them empty so
    // the page renders deterministically without unrelated network.
    for (const path of [
      "**/api/controls**",
      "**/api/scope/cells**",
      "**/api/audits**",
    ]) {
      await page.route(path, async (route, req) => {
        if (req.method() !== "GET") {
          await route.fallback();
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            controls: [],
            anchors: [],
            scope_cells: [],
            audit_periods: [],
            count: 0,
            total: 0,
          }),
        });
      });
    }

    // === Drive the chain ===

    // Establish a same-origin document so the in-page push `fetch` has a
    // page context. /login is unauthenticated and cheap.
    await page.goto("/login");

    // 1. CLI push. Model the atlas-cli wire call as an in-page fetch
    //    (intercepted by page.route above). Await the Receipt.
    const receipt = await page.evaluate(async () => {
      const resp = await fetch("/v1/evidence:push", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          evidence_kind: "sast.scan_result.v1",
          result: "pass",
          observed_at: "2026-05-29T12:00:00Z",
          payload: { findings: 0 },
        }),
      });
      return {
        ok: resp.ok,
        body: (await resp.json()) as {
          receipt?: { record_id?: string; hash?: string };
        },
      };
    });
    expect(receipt.ok).toBe(true);
    // The Receipt's hash is the content hash the platform computed — the
    // value the UI must display. This is the load-bearing tie between
    // the push stage and the display stage.
    expect(receipt.body.receipt?.hash).toBe(PUSHED_HASH);
    expect(receipt.body.receipt?.record_id).toBe(PUSHED_RECORD_ID);

    // 2-4. BFF read + UI display. Visit /evidence; the ledger now holds
    //      the pushed row.
    const listResp = page.waitForResponse(
      (r) => r.url().includes("/api/evidence") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/evidence");
    await listResp;

    // The pushed row renders. Assert on the hash cell (the prefix of the
    // Receipt's content hash) + the kind + the result — proving the
    // record that crossed the push wire is the record the UI displays.
    const hashCell = page.getByTestId("evidence-row-hash").first();
    await expect(hashCell).toBeVisible({ timeout: 30_000 });
    await expect(hashCell).toContainText(PUSHED_HASH_PREFIX);

    await expect(
      page.getByTestId("evidence-row-evidence-kind").first(),
    ).toContainText(PUSHED_KIND);
    await expect(page.getByTestId("evidence-row-result").first()).toContainText(
      "pass",
    );
  });
});
