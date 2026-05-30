// Slice 388 (closes slice 351 spillover, flow #12) — Playwright E2E for
// the board-pack EXPORT chain: generate → render → download-PDF.
//
// This is a v1 binary success-test flow (CLAUDE.md: "generate the next
// board pack from it"). Slice 333 Q-9 and slice 351's coverage matrix
// (flow #12, P1) both flag it as only half-covered:
// `board-pack-detail.spec.ts` exercises the UI render of a board pack but
// NOT the generate → render-PDF → download chain end-to-end. This spec
// closes that gap. It is ADDITIVE — it does not re-implement the detail
// spec, and it does not touch the board-narrative AI-assist surface
// (constitutional; out of scope per the slice doc).
//
// THE CHAIN MODELLED:
//
//   1. Generate. The operator POSTs a quarter-end date to the board-pack
//      list page; the BFF (`web/app/api/board-packs/route.ts`) forwards
//      to `POST /v1/board-packs`, returning a DRAFT BoardPack. We model
//      the wire call with an in-page `fetch` (page.evaluate) so it flows
//      through the page network and is intercepted by the route mock —
//      the `evidence-push-e2e.spec.ts` precedent — and assert the
//      returned pack id, which threads the rest of the chain (it is the
//      id the detail route renders and the PDF route streams).
//   2. Render. Navigate to `/board-packs/{id}`; the detail page issues
//      GET `/api/board-packs/{id}` (`getBoardPack`) + GET `/api/admin/me`
//      (the approver probe, decision D3 of slice 043). The page composes
//      the ExportBar, which carries the `export-pdf-link` anchor pointing
//      at the slice-043 BFF passthrough (`boardPackPdfURL(id)` →
//      `/api/board-packs/{id}/pdf`).
//   3. Download. Clicking the PDF link drives a request to the PDF BFF
//      route. That route (web/app/api/board-packs/[id]/pdf/route.ts) sets
//      `Content-Type: application/pdf` + `Content-Disposition: attachment;
//      filename="board-pack-{period}.pdf"` — the browser raises a
//      `download` event. We assert the suggested filename ends in `.pdf`
//      and that the bytes are the application/pdf payload the renderer
//      produced. The chromedp-backed PDF render (slices 340/341) lives
//      server-side in atlas; per the slice doc, when that render path
//      cannot be mocked meaningfully the spec is scoped to the BFF
//      generate + download-trigger boundary — which is exactly what this
//      mock asserts (the BFF byte passthrough + the attachment filename
//      contract). The chromedp PDF-bytes fidelity is its own real-stack
//      concern, already pinned by the slices 340/341 chromedp specs.
//
// MOCK STRATEGY (P0-4 of slice 351 — established `route.fulfill`
// convention): the e2e suite mocks the atlas/BFF wire surface so specs
// are deterministic without a per-spec SQL fixture. This spec follows the
// `evidence-push-e2e.spec.ts` precedent: the load-bearing assertions (the
// generated pack id threads through to the PDF route; the download fires
// with a `.pdf` filename) are preserved exactly; only the transport is
// mocked.
//
// Determinism (AC-3): the generate POST is awaited via an in-page
// `fetch`; the detail render is gated on
// `page.waitForResponse('/api/board-packs/{id}')` before any visibility
// assertion; the download is captured with
// `context.waitForEvent('download')` set up BEFORE the click (the link is
// `target="_blank"`, so the download surfaces on a popup context). No
// sleeps; no `.count()` snapshots.
//
// Routing scope: all mocks register on the browser CONTEXT (not the page)
// so the `target="_blank"` popup's PDF request is intercepted too.
//
// Hard rule (P0-A9): all ids below are neutral test strings. No
// vendor-prefixed tokens.

import { expect, test } from "./fixtures";

import {
  BOARD_PACK_SECTION_KEYS,
  type BoardPack,
  type BoardPackSection,
} from "@/lib/api/board";

// Neutral test ids. The pack id threads the whole chain: it is what the
// generate POST returns, what the detail page fetches, and what the PDF
// link / PDF route resolve.
const PACK_ID = "00000000-0000-0000-0000-0000000bp388";
const PERIOD_END = "2026-03-31"; // a calendar-quarter end (Q1 2026)
const PDF_FILENAME = `board-pack-${PERIOD_END}.pdf`;

// A minimal-but-type-conformant section. Every BOARD_PACK_SECTION_KEYS
// entry gets one so the detail page's `BOARD_PACK_SECTION_KEYS.map(...)`
// renders every SectionCard without a missing-key crash (the slice 276
// missing-required-field lesson: satisfy the producer type, not just the
// fields the test reads).
function section(key: string): BoardPackSection {
  return {
    key,
    title: key.replace(/_/g, " "),
    templated_text: `Templated narrative for ${key}.`,
    override_text: "",
    approved: false,
    data: {},
  };
}

// A DRAFT BoardPack matching the producer-side `BoardPack` type. This is
// both the generate POST response and the detail-route GET response (a
// freshly-generated pack is rendered immediately).
function draftPack(): BoardPack {
  const sections: Record<string, BoardPackSection> = {};
  for (const key of BOARD_PACK_SECTION_KEYS) sections[key] = section(key);
  return {
    id: PACK_ID,
    period_end: PERIOD_END,
    status: "draft",
    content: {
      period_end: PERIOD_END,
      generated_at: "2026-03-31T12:00:00Z",
      status: "draft",
      sections,
    },
    narrative_md: "# Board pack\n\nTemplated narrative.\n",
    created_at: "2026-03-31T12:00:00Z",
    updated_at: "2026-03-31T12:00:00Z",
  };
}

// Deterministic PDF payload. A real chromedp render returns many KB of
// binary; for the download-trigger boundary the load-bearing facts are
// the `application/pdf` content-type and the `attachment` filename, so a
// tiny valid-enough PDF header suffices. The %PDF magic keeps it honest
// as a PDF byte stream rather than an opaque blob.
const PDF_BYTES =
  "%PDF-1.7\n% slice-388 e2e board-pack export fixture\n%%EOF\n";

test.describe("board-pack export end-to-end (slice 388 — closes slice 351 #12)", () => {
  test("AC-2/AC-3: generate -> render -> download fires a .pdf attachment", async ({
    authedPage: page,
  }) => {
    // Register every mock on the browser CONTEXT, not the page. The PDF
    // export link carries `target="_blank"`, so clicking it opens a popup
    // whose request to the PDF BFF route must ALSO be intercepted —
    // `page.route` is page-scoped and would let the popup's request fall
    // through to the real (unauthenticated-locally) BFF. `context.route`
    // covers the original page AND every popup it opens.
    const context = page.context();

    // === Stage 1 — Generate (POST /api/board-packs -> DRAFT pack). ===
    // The BFF list route forwards to POST /v1/board-packs. We intercept
    // the BFF surface and return a DRAFT pack carrying PACK_ID — the id
    // that threads the render + download stages.
    await context.route("**/api/board-packs", async (route, req) => {
      if (req.method() === "POST") {
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify(draftPack()),
        });
        return;
      }
      // GET (the list query, if the page issues one) — return the pack
      // in a `{ packs: [...] }` envelope (listBoardPacks unwraps it).
      if (req.method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ packs: [draftPack()] }),
        });
        return;
      }
      await route.fallback();
    });

    // === Stage 2 — Render (GET /api/board-packs/{id} + /api/admin/me). ===
    // The detail page calls getBoardPack(id) and the approver probe
    // (getSessionMe → GET /api/admin/me; decision D3 of slice 043). The
    // generated DRAFT pack renders immediately; the ExportBar exposes the
    // PDF link. canApprove is irrelevant to the export path, so the probe
    // returns a non-admin shape (the PDF/markdown links render regardless).
    await context.route(`**/api/board-packs/${PACK_ID}`, async (route, req) => {
      if (req.method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(draftPack()),
      });
    });
    await context.route("**/api/admin/me", async (route, req) => {
      if (req.method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ is_admin: false }),
      });
    });

    // === Stage 3 — Download (GET /api/board-packs/{id}/pdf -> bytes). ===
    // The slice-043 PDF BFF route streams application/pdf bytes with a
    // Content-Disposition attachment header. Clicking the link makes the
    // browser raise a `download`. Mock the BFF byte passthrough verbatim,
    // including the attachment filename contract.
    await context.route(
      `**/api/board-packs/${PACK_ID}/pdf`,
      async (route, req) => {
        expect(req.method()).toBe("GET");
        await route.fulfill({
          status: 200,
          contentType: "application/pdf",
          headers: {
            "Content-Disposition": `attachment; filename="${PDF_FILENAME}"`,
          },
          body: PDF_BYTES,
        });
      },
    );

    // === Drive the chain ===

    // Establish a same-origin authed document so the in-page generate
    // request flows through the page network (and the page.route mocks).
    // The board-packs list page is the natural generate entry-point.
    await page.goto("/board-packs");

    // 1. Generate. Model the operator's "Generate draft" POST as an
    //    IN-PAGE fetch (page.evaluate) so it flows through the page's
    //    network and is intercepted by the page.route POST mock above —
    //    the `evidence-push-e2e.spec.ts` precedent. (`page.request.post`
    //    uses a separate APIRequestContext that page.route does NOT
    //    intercept.) Await the DRAFT pack and assert its id — the value
    //    that threads the render + download stages.
    const generated = await page.evaluate(async () => {
      const resp = await fetch("/api/board-packs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ period_end: "2026-03-31" }),
      });
      return {
        ok: resp.ok,
        body: (await resp.json()) as {
          id?: string;
          status?: string;
          period_end?: string;
        },
      };
    });
    expect(generated.ok).toBe(true);
    expect(generated.body.id).toBe(PACK_ID);
    expect(generated.body.status).toBe("draft");
    expect(generated.body.period_end).toBe(PERIOD_END);

    // 2. Render. Navigate to the generated pack's detail page; gate the
    //    first visibility assertion on the detail GET round-trip (the
    //    slice 275 pattern — waitForResponse set up BEFORE goto).
    const detailResp = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/board-packs/${PACK_ID}`) &&
        !r.url().includes("/pdf") &&
        !r.url().includes("/markdown") &&
        r.request().method() === "GET" &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto(`/board-packs/${generated.body.id}`);
    await detailResp;

    // The board-pack view and its sticky export bar render. The PDF link
    // is the export affordance under test.
    await expect(page.getByTestId("board-pack-view")).toBeVisible({
      timeout: 30_000,
    });
    const pdfLink = page.getByTestId("export-pdf-link");
    await expect(pdfLink).toBeVisible({ timeout: 30_000 });
    // The link points at the slice-043 BFF passthrough (not the raw
    // /v1/... endpoint a plain <a> cannot authorize).
    await expect(pdfLink).toHaveAttribute(
      "href",
      `/api/board-packs/${PACK_ID}/pdf`,
    );

    // 3. Download. Arm the download listener BEFORE the click (a
    //    Playwright invariant), then click the export link. The PDF link
    //    carries `target="_blank"`, so the attachment response may surface
    //    the download on a freshly-opened popup context rather than the
    //    originating page; listen on the browser CONTEXT (which catches
    //    downloads from any page/popup in it) to close that race. Assert
    //    the suggested filename ends in `.pdf` (AC-2) and resolve the
    //    bytes (proving the download completed deterministically rather
    //    than hanging — AC-3).
    const downloadPromise = page
      .context()
      .waitForEvent("download", { timeout: 30_000 });
    await pdfLink.click();
    const download = await downloadPromise;

    expect(download.suggestedFilename()).toBe(PDF_FILENAME);
    expect(download.suggestedFilename()).toMatch(/\.pdf$/);

    // The download stream resolves to the application/pdf bytes the BFF
    // streamed — the end of the export chain. Reading the stream to
    // completion is the deterministic "the download fired and finished"
    // assertion (no sleep).
    const stream = await download.createReadStream();
    const chunks: Buffer[] = [];
    for await (const c of stream) chunks.push(c as Buffer);
    const body = Buffer.concat(chunks).toString("utf-8");
    expect(body.startsWith("%PDF")).toBe(true);
  });
});
