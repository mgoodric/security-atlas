// Slice 757 — Playwright E2E for the batch review + per-answer approval
// queue (AC-9 / AC-10).
//
// Walks the batch flow end-to-end: import → "Draft all answers" (slice-756
// run) → the review queue renders every draft WITH its citations and model
// provenance → approve one (approver recorded) → edit-approve one (the edited
// text is what approval stores) → reject one (the question returns to
// unanswered) → the non-draft outcomes are their own work lists.
//
// Hermetic like the slice-441 AI spec in questionnaires.spec.ts: every BFF
// call is route-mocked with deterministic stub-LLM-shaped payloads (the
// shared docker-compose seed ships no questionnaire fixture, and the real
// atlas binary routes inference to Ollama — the established questionnaire
// e2e convention is the route-mock, recorded in the slice-757 decisions
// log). The reject/approve CONTRACT against the real store is proven by the
// Go integration tier (handlers_ai_reject_integration_test.go).
//
// Constitutional assertions carried here:
//   - AC-10 / P0-757-1: every approve request observed on the wire carries
//     EXACTLY ONE answer id (a string, never an array), and the surface
//     renders exactly one approve control (the focused draft's) — no
//     bulk/select-all affordance exists.
//   - P0-757-2: every rendered draft shows its citation links.
//   - AC-4 / P0-757-3: a cloud-routed draft renders the per-draft routing
//     banner; local drafts do not.
//   - P0-757-5: the suppressed work list renders the fixed reason copy only.

import { expect, test } from "./fixtures";

const Q_ID = "00000000-0000-0000-0000-0000000q7q57";
const Q_NAME = "Globex Vendor Security Review";
const POLICY_ID = "33333333-3333-3333-3333-333333333333";
const RUN_ID = "00000000-0000-0000-0000-00000run0757";

// Six imported questions: three draftable, one insufficient-evidence, one
// suppressed, one unmapped.
const QUESTIONS = [
  {
    id: "00000000-0000-0000-0000-0000000q0001",
    code: "IAM-01",
    text: "Is MFA required for all administrative access?",
  },
  {
    id: "00000000-0000-0000-0000-0000000q0002",
    code: "CRY-01",
    text: "Is customer data encrypted at rest?",
  },
  {
    id: "00000000-0000-0000-0000-0000000q0003",
    code: "LOG-01",
    text: "Are audit logs retained for at least one year?",
  },
  {
    id: "00000000-0000-0000-0000-0000000q0004",
    code: "BCP-01",
    text: "Do you test your disaster recovery plan annually?",
  },
  {
    id: "00000000-0000-0000-0000-0000000q0005",
    code: "VUL-01",
    text: "Do you run authenticated vulnerability scans?",
  },
  {
    id: "00000000-0000-0000-0000-0000000q0006",
    code: "HRS-01",
    text: "Are background checks performed before hire?",
  },
] as const;

const DRAFT_IDS = [
  "00000000-0000-0000-0000-000000dr0001",
  "00000000-0000-0000-0000-000000dr0002",
  "00000000-0000-0000-0000-000000dr0003",
] as const;

interface AnswerState {
  id: string;
  narrative: string;
  approved: boolean;
  approver?: string;
  provider: string;
}

// Mutable per-question answer state — the detail GET projects it, the
// approve/reject mocks mutate it, mirroring the server contract.
type AnswerMap = Map<string, AnswerState>;

function detailBody(answers: AnswerMap) {
  return {
    questionnaire: {
      id: Q_ID,
      name: Q_NAME,
      source_label: "CAIQ",
      source_filename: "globex-caiq.xlsx",
      status: "draft",
      created_at: "2026-08-01T18:00:00Z",
      updated_at: "2026-08-01T18:00:00Z",
    },
    questions: QUESTIONS.map((q, i) => {
      const a = answers.get(q.id);
      return {
        id: q.id,
        code: q.code,
        text: q.text,
        domain: q.code.slice(0, 3),
        answer_type: "yes_no",
        scf_anchor_id: q.code === "HRS-01" ? null : "IAC-06",
        sort_order: i + 1,
        needs_mapping: q.code === "HRS-01",
        answer: a
          ? {
              id: a.id,
              answer_value: "",
              narrative: a.narrative,
              citations: [{ kind: "policy", id: POLICY_ID }],
              authored_by: "",
              ai_assisted: true,
              human_approved: a.approved,
              human_approver: a.approver ?? "",
              prompt_version: "qaisuggest-v1",
              model_name: "llama3.1:8b-instruct-q5",
              model_version: "1",
              model_provider: a.provider,
            }
          : undefined,
      };
    }),
  };
}

function runDetailBody() {
  return {
    run: {
      id: RUN_ID,
      questionnaire_id: Q_ID,
      status: "completed",
      started_by: "key_grc",
      row_cap: 500,
      total_count: 6,
      drafted_count: 3,
      insufficient_count: 1,
      suppressed_count: 1,
      skipped_count: 1,
      error_count: 0,
    },
    items: [
      {
        id: "it-1",
        run_id: RUN_ID,
        questionnaire_id: Q_ID,
        question_id: QUESTIONS[0].id,
        sort_order: 1,
        outcome: "drafted",
        answer_id: DRAFT_IDS[0],
      },
      {
        id: "it-2",
        run_id: RUN_ID,
        questionnaire_id: Q_ID,
        question_id: QUESTIONS[1].id,
        sort_order: 2,
        outcome: "drafted",
        answer_id: DRAFT_IDS[1],
      },
      {
        id: "it-3",
        run_id: RUN_ID,
        questionnaire_id: Q_ID,
        question_id: QUESTIONS[2].id,
        sort_order: 3,
        outcome: "drafted",
        answer_id: DRAFT_IDS[2],
      },
      {
        id: "it-4",
        run_id: RUN_ID,
        questionnaire_id: Q_ID,
        question_id: QUESTIONS[3].id,
        sort_order: 4,
        outcome: "insufficient_evidence",
        reason_code: "no_candidates",
      },
      {
        id: "it-5",
        run_id: RUN_ID,
        questionnaire_id: Q_ID,
        question_id: QUESTIONS[4].id,
        sort_order: 5,
        outcome: "suppressed",
        reason_code: "unresolved_citation",
      },
      {
        id: "it-6",
        run_id: RUN_ID,
        questionnaire_id: Q_ID,
        question_id: QUESTIONS[5].id,
        sort_order: 6,
        outcome: "skipped_needs_mapping",
        reason_code: "needs_mapping",
      },
    ],
  };
}

test.describe("questionnaire batch review queue (slice 757)", () => {
  test("AC-9: import → draft-all → review with citations → approve / edit-approve / reject → outcome lists", async ({
    authedPage: page,
  }) => {
    const answers: AnswerMap = new Map();
    // Wire-level captures for the AC-10 assertion.
    const approveBodies: unknown[] = [];
    const rejectBodies: unknown[] = [];

    // --- list / create / import (the import leg of AC-9) ---
    await page.route("**/api/questionnaires", async (route, req) => {
      if (req.method() === "POST") {
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
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ questionnaires: [] }),
      });
    });
    await page.route(
      `**/api/questionnaires/${Q_ID}/import-excel`,
      async (route) => {
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({
            questions: detailBody(answers).questions,
            unmapped_columns: [],
          }),
        });
      },
    );

    // --- detail: projects the mutable answer state ---
    await page.route(`**/api/questionnaires/${Q_ID}`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(detailBody(answers)),
      });
    });

    // Authoring-view collaborators (mounted briefly before the review view).
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
    await page.route("**/api/admin/llm-routing", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          provider: "local-ollama",
          is_cloud: false,
          has_api_key: false,
        }),
      });
    });

    // --- slice-756 run: POST completes the run and lands three drafts.
    //     Draft 3 is CLOUD-ROUTED (per-draft banner, AC-4). ---
    await page.route(
      `**/api/questionnaires/${Q_ID}/answer-runs`,
      async (route) => {
        answers.set(QUESTIONS[0].id, {
          id: DRAFT_IDS[0],
          narrative: `Yes. MFA is enforced for all administrative access (policy ${POLICY_ID}).`,
          approved: false,
          provider: "ollama-local",
        });
        answers.set(QUESTIONS[1].id, {
          id: DRAFT_IDS[1],
          narrative: `Yes. Customer data is encrypted at rest with AES-256 (policy ${POLICY_ID}).`,
          approved: false,
          provider: "ollama-local",
        });
        answers.set(QUESTIONS[2].id, {
          id: DRAFT_IDS[2],
          narrative: `Yes. Audit logs are retained for 365 days (policy ${POLICY_ID}).`,
          approved: false,
          provider: "anthropic",
        });
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(runDetailBody()),
        });
      },
    );
    await page.route(
      `**/api/questionnaires/${Q_ID}/answer-runs/${RUN_ID}`,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(runDetailBody()),
        });
      },
    );

    // --- per-answer approve / reject: mutate the answer state the way the
    //     platform does, and capture the wire bodies for AC-10. ---
    await page.route(
      `**/api/questionnaires/${Q_ID}/answers/*/ai-approve`,
      async (route, req) => {
        const body = JSON.parse(req.postData() ?? "{}") as {
          answer_id?: unknown;
          narrative?: string;
        };
        approveBodies.push(body);
        const entry = [...answers.values()].find(
          (a) => a.id === body.answer_id,
        );
        if (entry) {
          entry.approved = true;
          entry.approver = "key_grc";
          entry.narrative = body.narrative ?? entry.narrative;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            answer_id: body.answer_id,
            narrative: body.narrative ?? "",
            answer_value: "",
            human_approved: true,
            human_approver: "key_grc",
          }),
        });
      },
    );
    await page.route(
      `**/api/questionnaires/${Q_ID}/answers/*/ai-reject`,
      async (route, req) => {
        const body = JSON.parse(req.postData() ?? "{}") as {
          answer_id?: unknown;
        };
        rejectBodies.push(body);
        for (const [qid, a] of answers) {
          if (a.id === body.answer_id) answers.delete(qid);
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            answer_id: body.answer_id,
            question_id: QUESTIONS[2].id,
            status: "rejected",
          }),
        });
      },
    );

    // === Import ===
    const importResp = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/questionnaires/${Q_ID}/import-excel`) &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.goto("/questionnaires");
    await expect(page.getByTestId("questionnaire-upload-zone")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId("questionnaire-upload-input").setInputFiles({
      name: "globex-caiq.xlsx",
      mimeType:
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      buffer: Buffer.from("pretend-xlsx-bytes"),
    });
    await importResp;
    await expect(page).toHaveURL(new RegExp(`/questionnaires/${Q_ID}$`), {
      timeout: 30_000,
    });

    // === Draft all answers (slice-756 run) ===
    const runResp = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/questionnaires/${Q_ID}/answer-runs`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("questionnaire-draft-all").click();
    await runResp;

    // The page routes into the review view with the run bound in the URL
    // (AC-7 resumability: view + run id both live in the URL).
    await expect(page).toHaveURL(
      new RegExp(`/questionnaires/${Q_ID}\\?view=review&run=${RUN_ID}`),
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("review-queue")).toBeVisible({
      timeout: 30_000,
    });

    // Run progress by outcome (AC-2).
    await expect(page.getByTestId("run-progress-drafted")).toContainText("3");
    await expect(
      page.getByTestId("run-progress-insufficient_evidence"),
    ).toContainText("1");
    await expect(page.getByTestId("run-progress-suppressed")).toContainText(
      "1",
    );

    // === Queue renders draft 1 with citations + provenance (AC-3) ===
    await expect(page.getByTestId("review-position")).toContainText("1 / 3");
    await expect(page.getByTestId("review-question")).toContainText(
      "MFA required",
    );
    await expect(page.getByTestId("review-provenance")).toContainText(
      "llama3.1:8b-instruct-q5",
    );
    const citation = page.getByTestId("review-citation-link");
    await expect(citation).toHaveCount(1);
    await expect(citation).toHaveAttribute("href", `/policies/${POLICY_ID}`);
    // Local draft: NO per-draft cloud banner.
    await expect(page.getByTestId("review-cloud-banner")).toHaveCount(0);

    // AC-10 / P0-757-1 structural check: exactly ONE approve control on the
    // surface, and no bulk affordance anywhere.
    await expect(page.getByTestId("review-approve")).toHaveCount(1);
    await expect(
      page.getByRole("button", { name: /approve all|select all|bulk/i }),
    ).toHaveCount(0);

    // === Approve draft 1 (one click, approver recorded) ===
    const approve1 = page.waitForResponse(
      (r) => r.url().includes("/ai-approve") && r.status() === 200,
      { timeout: 30_000 },
    );
    const refetch1 = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/questionnaires/${Q_ID}`) && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("review-approve").click();
    await approve1;
    await refetch1;
    // The approved answer leaves the queue (AC-7).
    await expect(page.getByTestId("review-position")).toContainText("1 / 2", {
      timeout: 30_000,
    });

    // === Edit-approve draft 2: the edited text is what approval stores ===
    await expect(page.getByTestId("review-question")).toContainText(
      "encrypted at rest",
    );
    const edited = `Yes — all customer data is encrypted at rest using AES-256-GCM per our cryptography policy (policy ${POLICY_ID}).`;
    await page.getByTestId("review-draft-text").fill(edited);
    const approve2 = page.waitForResponse(
      (r) => r.url().includes("/ai-approve") && r.status() === 200,
      { timeout: 30_000 },
    );
    const refetch2 = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/questionnaires/${Q_ID}`) && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("review-approve").click();
    await approve2;
    await refetch2;
    await expect(page.getByTestId("review-position")).toContainText("1 / 1", {
      timeout: 30_000,
    });

    // === Reject draft 3 (cloud-routed: the per-draft banner shows, AC-4) ===
    await expect(page.getByTestId("review-question")).toContainText(
      "audit logs retained",
    );
    await expect(page.getByTestId("review-cloud-banner")).toBeVisible();
    const reject3 = page.waitForResponse(
      (r) => r.url().includes("/ai-reject") && r.status() === 200,
      { timeout: 30_000 },
    );
    const refetch3 = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/questionnaires/${Q_ID}`) && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.getByTestId("review-reject").click();
    await reject3;
    await refetch3;
    // Queue drained.
    await expect(page.getByTestId("review-empty")).toBeVisible({
      timeout: 30_000,
    });

    // === Non-draft outcome work lists (AC-6) ===
    await page.getByTestId("review-tab-insufficient_evidence").click();
    await expect(
      page.getByTestId("outcome-row-insufficient_evidence"),
    ).toContainText("disaster recovery");
    await expect(page.getByTestId("outcome-answer-manually")).toBeVisible();

    await page.getByTestId("review-tab-skipped_needs_mapping").click();
    await expect(
      page.getByTestId("outcome-row-skipped_needs_mapping"),
    ).toContainText("background checks");
    await expect(page.getByTestId("outcome-map-question")).toBeVisible();

    await page.getByTestId("review-tab-suppressed").click();
    const suppressedReason = page.getByTestId("outcome-suppressed-reason");
    // Fixed reason copy only (P0-757-5) — never the raw code or model detail.
    await expect(suppressedReason).toHaveText(
      "Draft withheld: a citation could not be verified.",
    );

    // === Outcomes reflected in the questionnaire (back in authoring) ===
    await page.getByTestId("questionnaire-back-to-authoring").click();
    await expect(page.getByTestId("question-list-pane")).toBeVisible({
      timeout: 30_000,
    });

    // === AC-10: every observed approval carried exactly one answer id ===
    expect(approveBodies).toHaveLength(2);
    for (const b of approveBodies) {
      const ids = (b as { answer_id?: unknown }).answer_id;
      expect(typeof ids).toBe("string");
      expect(Array.isArray(ids)).toBe(false);
    }
    expect(approveBodies[0]).toMatchObject({ answer_id: DRAFT_IDS[0] });
    // The edit-approve stored the EDITED narrative (AC-5).
    expect(approveBodies[1]).toMatchObject({
      answer_id: DRAFT_IDS[1],
      narrative: edited,
    });
    expect(rejectBodies).toEqual([{ answer_id: DRAFT_IDS[2] }]);
  });
});
