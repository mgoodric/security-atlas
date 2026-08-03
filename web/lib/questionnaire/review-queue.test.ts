// Slice 757 — vitest coverage for the review-queue view-model. The
// constitutional gates live here (AC-10 / P0-757-1 / P0-757-2 / P0-757-5):
// per-answer request builders, citation-gated approvability, fixed
// suppression copy, provider-derived cloud routing.

import { describe, expect, test } from "vitest";

import type { Question } from "../../components/questionnaire/types";
import {
  type AnswerRunDetail,
  approveRequestBody,
  citationLinks,
  isCloudProvider,
  modelLabel,
  outcomeRows,
  pendingDrafts,
  rejectRequestBody,
  runProgress,
  suppressedReasonLabel,
} from "./review-queue";

function question(overrides: Partial<Question> & { id: string }): Question {
  return {
    code: "Q-1",
    text: "Do you encrypt data at rest?",
    domain: "IAM",
    answer_type: "yes_no",
    scf_anchor_id: "IAC-06",
    sort_order: 1,
    needs_mapping: false,
    ...overrides,
  } as Question;
}

const draftAnswer = {
  id: "answer-1",
  answer_value: "",
  narrative: "Yes, AES-256 at rest.",
  citations: [{ kind: "policy", id: "33333333-3333-3333-3333-333333333333" }],
  ai_assisted: true,
  human_approved: false,
  model_name: "llama3.1:8b-instruct-q5",
  model_version: "1",
  model_provider: "ollama-local",
  prompt_version: "qaisuggest-v0",
};

describe("pendingDrafts", () => {
  test("admits only unapproved AI drafts", () => {
    const qs: Question[] = [
      question({ id: "q1", answer: draftAnswer as never }),
      // Approved AI answer — left the queue (AC-7).
      question({
        id: "q2",
        answer: {
          ...draftAnswer,
          id: "answer-2",
          human_approved: true,
          human_approver: "key_grc",
        } as never,
      }),
      // Manual answer — never enters the queue.
      question({
        id: "q3",
        answer: {
          id: "answer-3",
          answer_value: "Yes",
          narrative: "manual",
          citations: [],
          ai_assisted: false,
        } as never,
      }),
      // Unanswered — nothing to review.
      question({ id: "q4" }),
    ];
    const drafts = pendingDrafts(qs);
    expect(drafts).toHaveLength(1);
    expect(drafts[0]?.answerId).toBe("answer-1");
    expect(drafts[0]?.canApprove).toBe(true);
    expect(drafts[0]?.modelLabel).toContain("llama3.1:8b-instruct-q5");
    expect(drafts[0]?.promptVersion).toBe("qaisuggest-v0");
  });

  test("a draft without citations is not approvable (P0-757-2)", () => {
    const qs = [
      question({
        id: "q1",
        answer: { ...draftAnswer, citations: [] } as never,
      }),
    ];
    const drafts = pendingDrafts(qs);
    expect(drafts).toHaveLength(1);
    expect(drafts[0]?.canApprove).toBe(false);
  });

  test("cloud provider flags cloudRouted; local does not (AC-4)", () => {
    const local = pendingDrafts([
      question({ id: "q1", answer: draftAnswer as never }),
    ]);
    expect(local[0]?.cloudRouted).toBe(false);
    const cloud = pendingDrafts([
      question({
        id: "q1",
        answer: { ...draftAnswer, model_provider: "anthropic-api" } as never,
      }),
    ]);
    expect(cloud[0]?.cloudRouted).toBe(true);
  });
});

describe("isCloudProvider + modelLabel", () => {
  test("mirrors the Go provider classification", () => {
    for (const local of [
      "",
      "ollama",
      "ollama-local",
      "local",
      "stub",
      "STUB",
    ]) {
      expect(isCloudProvider(local)).toBe(false);
    }
    for (const cloud of ["anthropic-api", "openai", "bedrock"]) {
      expect(isCloudProvider(cloud)).toBe(true);
    }
    expect(isCloudProvider(undefined)).toBe(false);
  });

  test("modelLabel composes name, version, provider", () => {
    expect(modelLabel("m", "2", "ollama-local")).toBe("m v2 (ollama-local)");
    expect(modelLabel(undefined, "2", "x")).toBe("");
  });
});

describe("per-answer request builders (AC-10)", () => {
  test("approve carries exactly one answer id", () => {
    const body = approveRequestBody("answer-1", "edited text");
    expect(body.answer_id).toBe("answer-1");
    expect(typeof body.answer_id).toBe("string");
    expect(body.narrative).toBe("edited text");
    // Structural: the payload has no array-valued field — a bulk approval
    // cannot be expressed through this builder.
    for (const v of Object.values(body)) {
      expect(Array.isArray(v)).toBe(false);
    }
  });

  test("reject carries exactly one answer id", () => {
    const body = rejectRequestBody("answer-1");
    expect(body).toEqual({ answer_id: "answer-1" });
  });
});

describe("citationLinks", () => {
  test("maps AI-draft and manual citation shapes to resolving links", () => {
    const links = citationLinks([
      { kind: "policy", id: "aaaaaaaa-0000-0000-0000-000000000000" },
      { kind: "evidence", id: "bbbbbbbb-0000-0000-0000-000000000000" },
      {
        type: "controls",
        id: "cccccccc-0000-0000-0000-000000000000",
        title: "MFA",
      },
    ]);
    expect(links).toHaveLength(3);
    expect(links[0]?.href).toBe(
      "/policies/aaaaaaaa-0000-0000-0000-000000000000",
    );
    expect(links[1]?.href).toBe("/evidence");
    expect(links[2]?.href).toBe(
      "/controls/cccccccc-0000-0000-0000-000000000000",
    );
  });

  test("drops unknown shapes instead of rendering dead links", () => {
    expect(
      citationLinks([{ kind: "mystery", id: "x" }, null, "str", {}]),
    ).toEqual([]);
    expect(citationLinks("not-an-array")).toEqual([]);
  });
});

describe("suppressedReasonLabel (P0-757-5)", () => {
  test("maps the fixed vocabulary and never echoes unknown codes", () => {
    expect(suppressedReasonLabel("unresolved_citation")).toContain("citation");
    expect(suppressedReasonLabel("no_citations")).toContain("no citations");
    expect(suppressedReasonLabel("generation_unavailable")).toContain(
      "unavailable",
    );
    // An unknown/backend-detail code renders the generic line — the code
    // itself never reaches the copy.
    const label = suppressedReasonLabel("ollama: connection refused :11434");
    expect(label).toBe("Draft withheld.");
    expect(label).not.toContain("11434");
  });
});

describe("outcomeRows + runProgress", () => {
  const detail: AnswerRunDetail = {
    run: {
      id: "run-1",
      questionnaire_id: "qn-1",
      status: "completed",
      started_by: "key_grc",
      row_cap: 100,
      total_count: 4,
      drafted_count: 1,
      insufficient_count: 1,
      suppressed_count: 1,
      skipped_count: 1,
      error_count: 0,
    },
    items: [
      {
        id: "i1",
        run_id: "run-1",
        questionnaire_id: "qn-1",
        question_id: "q1",
        sort_order: 1,
        outcome: "drafted",
        answer_id: "answer-1",
      },
      {
        id: "i2",
        run_id: "run-1",
        questionnaire_id: "qn-1",
        question_id: "q2",
        sort_order: 2,
        outcome: "insufficient_evidence",
        reason_code: "insufficient_evidence",
      },
      {
        id: "i3",
        run_id: "run-1",
        questionnaire_id: "qn-1",
        question_id: "q3",
        sort_order: 3,
        outcome: "suppressed",
        reason_code: "unresolved_citation",
      },
      {
        id: "i4",
        run_id: "run-1",
        questionnaire_id: "qn-1",
        question_id: "q4",
        sort_order: 4,
        outcome: "skipped_needs_mapping",
        reason_code: "needs_mapping",
      },
    ],
  };
  const qs = [
    question({ id: "q1" }),
    question({ id: "q2", code: "Q-2" }),
    question({ id: "q3", code: "Q-3" }),
    question({ id: "q4", code: "Q-4", needs_mapping: true }),
  ];

  test("filters items per outcome and joins question metadata", () => {
    const insufficient = outcomeRows(detail, qs, "insufficient_evidence");
    expect(insufficient).toHaveLength(1);
    expect(insufficient[0]?.questionCode).toBe("Q-2");

    const suppressed = outcomeRows(detail, qs, "suppressed");
    expect(suppressed).toHaveLength(1);
    expect(suppressed[0]?.reasonLabel).toContain("citation");

    const needsMapping = outcomeRows(detail, qs, "skipped_needs_mapping");
    expect(needsMapping).toHaveLength(1);
    expect(needsMapping[0]?.questionCode).toBe("Q-4");
  });

  test("null run detail yields empty lists", () => {
    expect(outcomeRows(null, qs, "suppressed")).toEqual([]);
  });

  test("runProgress surfaces counts by outcome (AC-2)", () => {
    const entries = runProgress(detail.run);
    expect(entries.find((e) => e.key === "drafted")?.count).toBe(1);
    expect(entries.find((e) => e.key === "suppressed")?.count).toBe(1);
    expect(entries).toHaveLength(5);
  });
});
