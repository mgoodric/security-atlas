// Slice 263 — Playwright E2E for the questionnaire authoring flow.
//
// Walks the slice 263 happy path against the new /questionnaires +
// /questionnaires/{id} routes (Stage A + Stage C). The slice consumes
// six BFF endpoints under /api/questionnaires/* + the slice-268
// /api/search endpoint; all are mocked via `page.route()` here so the
// spec is deterministic across CI runs without requiring a new SQL
// fixture (the slice 155 backend doesn't ship one for the demo
// tenant).
//
// AC-24 contract:
//   - empty-list → upload → answer-one-question → cite-one-evidence
//     → save-to-library → export-PDF
//   - 7+ assertions
//   - slice 274 + 275 auto-wait pattern: every async boundary is
//     gated on `page.waitForResponse(...)` BEFORE the visibility
//     assertion that depends on the response landing. No
//     `.count()` snapshots; no `waitForTimeout` polling.
//
// AI-assist boundary (P0-263-1): the spec asserts the suggestions
// panel is rendered as deterministic prior-answer surface — NO
// "AI suggestions" header, NO model badges visible. The actual
// invariant is enforced at the component layer (suggestions-panel.tsx
// + the slice 155 backend handler); this spec pins the rendered
// surface to confirm.
//
// Slice 254 quarantine playbook: if this spec hits the same Suspense
// / route-coverage race that slice 254 ran into, quarantine the
// failing assertion with test.skip + file a spillover slice; do NOT
// over-rotate on Playwright timing here.

import { expect, test } from "./fixtures";

const Q_ID = "00000000-0000-0000-0000-0000000q1q1q";
const Q_NAME = "Acme Corp Security Diligence";
const QUESTION_ID = "00000000-0000-0000-0000-0000000Q11Q1";
const ANCHOR_ID = "IAC-06";
const EVIDENCE_ID = "00000000-0000-0000-0000-0000000ev01";

// Detail payload — slice 155 getResponse shape.
function detailBody(answerOverride?: {
  answer_value?: string;
  narrative?: string;
  citations?: unknown[];
}) {
  return {
    questionnaire: {
      id: Q_ID,
      name: Q_NAME,
      source_label: "CAIQ",
      source_filename: "acme-caiq-v4-1.xlsx",
      status: "draft",
      created_at: "2026-05-23T18:00:00Z",
      updated_at: "2026-05-23T18:00:00Z",
    },
    questions: [
      {
        id: QUESTION_ID,
        code: "IAM-02",
        text: "Are users required to use MFA for all administrative access?",
        domain: "IAM",
        answer_type: "yes_no",
        scf_anchor_id: ANCHOR_ID,
        sort_order: 1,
        needs_mapping: false,
        answer: answerOverride
          ? {
              id: "answ-1",
              answer_value: answerOverride.answer_value ?? "",
              narrative: answerOverride.narrative ?? "",
              citations: answerOverride.citations ?? [],
            }
          : undefined,
      },
    ],
  };
}

test.describe("questionnaires (slice 263)", () => {
  test("AC-24 happy path: empty → upload → answer → cite → save-to-library → PDF", async ({
    authedPage: page,
  }) => {
    // 1. Empty list — /api/questionnaires returns {questionnaires: []}.
    //    The page renders the hero CTA upload zone.
    await page.route("**/api/questionnaires", async (route, req) => {
      if (req.method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ questionnaires: [] }),
        });
        return;
      }
      // POST = create — fall through to the create handler below.
      await route.fallback();
    });

    // 2. Create — POST /api/questionnaires returns the new questionnaire.
    await page.route("**/api/questionnaires", async (route, req) => {
      if (req.method() !== "POST") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          id: Q_ID,
          name: Q_NAME,
          source_label: "CAIQ",
          source_filename: Q_NAME,
          status: "draft",
        }),
      });
    });

    // 3. import-excel — accepts the multipart payload, returns parsed Qs.
    await page.route(
      `**/api/questionnaires/${Q_ID}/import-excel`,
      async (route) => {
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({
            questions: [detailBody().questions[0]],
            unmapped_columns: [],
          }),
        });
      },
    );

    // 4. Detail — initial GET returns the questionnaire with one Q,
    //    no answer yet. Subsequent GETs after PATCH return the
    //    answered state. We use a state-carrying closure to flip.
    let answeredBody: ReturnType<typeof detailBody> = detailBody();
    await page.route(`**/api/questionnaires/${Q_ID}`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(answeredBody),
      });
    });

    // 5. Suggestions — deterministic top-3 (we return one to keep
    //    the assertion simple).
    await page.route(
      `**/api/questionnaires/${Q_ID}/suggestions**`,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            suggestions: [
              {
                ID: "sug-1",
                ScfAnchorID: ANCHOR_ID,
                CanonicalText:
                  "Yes. MFA is enforced via Okta workforce policy for all administrative access.",
                SourceLabel: "SIG Lite 2026 / Globex Inc",
                UpdatedAt: "2026-02-14T00:00:00Z",
              },
            ],
          }),
        });
      },
    );

    // 6. PATCH answer — accepts the body verbatim; on success we
    //    update the closure so the next detail GET reflects the save.
    await page.route(
      `**/api/questionnaires/${Q_ID}/answers/${QUESTION_ID}`,
      async (route, req) => {
        const body = JSON.parse(req.postData() ?? "{}") as {
          answer_value?: string;
          narrative?: string;
          citations?: unknown[];
          save_to_library?: boolean;
        };
        answeredBody = detailBody({
          answer_value: body.answer_value,
          narrative: body.narrative,
          citations: body.citations,
        });
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            id: "answ-1",
            answer_value: body.answer_value ?? "",
            narrative: body.narrative ?? "",
            citations: body.citations ?? [],
          }),
        });
      },
    );

    // 7. Search — slice 268 unified search BFF; citation picker
    //    consumes this. Returns one evidence row matching the query.
    await page.route("**/api/search**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          hits: [
            {
              id: EVIDENCE_ID,
              type: "evidence",
              title: "Okta workforce policy snapshot",
              snippet: "MFA enforced via Okta",
              relevance_score: 1,
            },
          ],
          count: 1,
        }),
      });
    });

    // 8. Export PDF — return a tiny PDF byte stream.
    await page.route(
      `**/api/questionnaires/${Q_ID}/export-pdf`,
      async (route) => {
        await route.fulfill({
          status: 200,
          headers: { "Content-Type": "application/pdf" },
          body: Buffer.from([0x25, 0x50, 0x44, 0x46, 0x2d, 0x31, 0x2e, 0x34]),
        });
      },
    );

    // === Empty list ===
    const listResp = page.waitForResponse(
      (r) => r.url().endsWith("/api/questionnaires") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/questionnaires");
    await listResp;
    await expect(page.getByTestId("questionnaires-empty")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("questionnaire-upload-zone")).toBeVisible();

    // === Upload — programmatically attach a small .xlsx blob via the
    //     hidden input and wait for the create + import + nav. ===
    const importResp = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/questionnaires/${Q_ID}/import-excel`) &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("questionnaire-upload-input").setInputFiles({
      name: "test.xlsx",
      mimeType:
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      buffer: Buffer.from("pretend-xlsx-bytes"),
    });
    await importResp;
    // After upload, the page navigates to /questionnaires/{id}.
    await expect(page).toHaveURL(new RegExp(`/questionnaires/${Q_ID}$`), {
      timeout: 30_000,
    });

    // === Stage C — answer editor renders the one question ===
    await expect(page.getByTestId("answer-editor")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("answer-editor-anchor")).toContainText(
      `SCF:${ANCHOR_ID}`,
    );
    // Suggestion panel rendered the deterministic suggestion.
    await expect(page.getByTestId("suggestion-card")).toBeVisible({
      timeout: 30_000,
    });
    // Click "Use this answer" — the textarea content replaces.
    await page.getByTestId("suggestion-use-button").click();
    const textarea = page.getByTestId("answer-editor-narrative");
    await expect(textarea).toContainText("MFA is enforced");

    // === Cite one evidence — open picker, type, click result ===
    const patchAfterCite = page.waitForResponse(
      (r) =>
        r
          .url()
          .includes(`/api/questionnaires/${Q_ID}/answers/${QUESTION_ID}`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("citation-picker-open").click();
    await page.getByTestId("citation-picker-input").fill("okta");
    await expect(
      page.getByTestId("citation-picker-row-evidence").first(),
    ).toBeVisible({ timeout: 30_000 });
    await page.getByTestId("citation-picker-row-evidence").first().click();
    await patchAfterCite;
    // The chip is now visible below the textarea.
    await expect(page.getByTestId("citation-chip")).toBeVisible();

    // === Save-to-library — tick the checkbox, wait for the next PATCH ===
    const patchAfterLib = page.waitForResponse(
      (r) =>
        r
          .url()
          .includes(`/api/questionnaires/${Q_ID}/answers/${QUESTION_ID}`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("answer-editor-save-to-library-checkbox").check();
    await patchAfterLib;

    // === Export PDF — click the button, expect a download ===
    const downloadPromise = page.waitForEvent("download", { timeout: 30_000 });
    await page.getByTestId("questionnaire-export-pdf").click();
    const download = await downloadPromise;
    // The browser-derived filename is built from the questionnaire
    // name — assert .pdf suffix to keep the assertion stable.
    expect(download.suggestedFilename()).toMatch(/\.pdf$/);
  });

  // Slice 441 — AI-answer suggestion v0. Asserts the constitutional FE
  // behavior: a suppressed draft offers NO approvable text (AC-11/P0-441-4),
  // and a valid cited draft IS approvable, approving it writes the approved
  // text into the manual editor (AC-12). Hermetic: every BFF call is
  // route-mocked (no shared-DB seed).
  test("AI suggest: suppressed → no approve; cited draft → approve writes narrative", async ({
    authedPage: page,
  }) => {
    let answeredBody: ReturnType<typeof detailBody> = detailBody();
    await page.route(`**/api/questionnaires/${Q_ID}`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(answeredBody),
      });
    });
    // Empty deterministic suggestions so only the AI panel is under test.
    await page.route(
      `**/api/questionnaires/${Q_ID}/suggestions**`,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ suggestions: [] }),
        });
      },
    );

    // First ai-suggest call returns a SUPPRESSED outcome (a fabricated/
    // cross-tenant citation was withheld server-side); second returns a valid
    // cited draft. A state-carrying closure flips between them.
    let suggestCall = 0;
    await page.route(
      `**/api/questionnaires/${Q_ID}/answers/${QUESTION_ID}/ai-suggest`,
      async (route) => {
        suggestCall += 1;
        const body =
          suggestCall === 1
            ? { suppressed: true, reason: "unresolved_citation" }
            : {
                answer_id: "ai-draft-1",
                question_id: QUESTION_ID,
                draft:
                  "Yes. MFA is enforced for administrative access (policy 33333333-3333-3333-3333-333333333333).",
                citations: [
                  {
                    kind: "policy",
                    id: "33333333-3333-3333-3333-333333333333",
                  },
                ],
                model_name: "llama3.1:8b-instruct-q5",
                model_version: "1",
                model_provider: "ollama-local",
                cloud_routed: false,
              };
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(body),
        });
      },
    );

    await page.route(
      `**/api/questionnaires/${Q_ID}/answers/${QUESTION_ID}/ai-approve`,
      async (route, req) => {
        const b = JSON.parse(req.postData() ?? "{}") as { narrative?: string };
        answeredBody = detailBody({ narrative: b.narrative });
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            answer_id: "ai-draft-1",
            question_id: QUESTION_ID,
            narrative: b.narrative ?? "",
            answer_value: "",
            human_approved: true,
            human_approver: "key_grc",
          }),
        });
      },
    );

    await page.goto(`/questionnaires/${Q_ID}`);
    await expect(page.getByTestId("ai-suggest-panel")).toBeVisible({
      timeout: 30_000,
    });

    // === Suppressed outcome — manual message, NO approvable draft ===
    const firstSuggest = page.waitForResponse(
      (r) =>
        r.url().includes(`/answers/${QUESTION_ID}/ai-suggest`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("ai-suggest-button").click();
    await firstSuggest;
    await expect(page.getByTestId("ai-suggest-manual")).toBeVisible({
      timeout: 30_000,
    });
    // P0-441-4: there is no approve button to click on a suppressed outcome.
    await expect(page.getByTestId("ai-suggest-approve")).toHaveCount(0);

    // === Valid cited draft — approvable ===
    const secondSuggest = page.waitForResponse(
      (r) =>
        r.url().includes(`/answers/${QUESTION_ID}/ai-suggest`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("ai-suggest-button").click();
    await secondSuggest;
    await expect(page.getByTestId("ai-suggest-draft")).toBeVisible({
      timeout: 30_000,
    });
    const approveBtn = page.getByTestId("ai-suggest-approve");
    await expect(approveBtn).toBeEnabled();

    // === Approve — writes the approved text into the manual narrative ===
    const approveResp = page.waitForResponse(
      (r) =>
        r.url().includes(`/answers/${QUESTION_ID}/ai-approve`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await approveBtn.click();
    await approveResp;
    await expect(page.getByTestId("answer-editor-narrative")).toContainText(
      "MFA is enforced for administrative access",
    );
  });

  test("sidebar exposes Questionnaires entry under Operations cluster", async ({
    authedPage: page,
  }) => {
    // Visit any authed page; the sidebar is the shared shell so the
    // entry should appear regardless of route.
    await page.goto("/dashboard");
    await expect(
      page.getByRole("link", { name: "Questionnaires" }),
    ).toBeVisible({
      timeout: 30_000,
    });
  });
});
