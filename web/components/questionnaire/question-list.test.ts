// Slice 263 — vitest coverage for the pure helpers in question-list.tsx.
//
// `statusForQuestion` + `groupByDomain` are exported as pure functions
// (no React) so we can pin the answer-status derivation rules + the
// domain-grouping shape without dragging in @testing-library/react
// (which is intentionally NOT a dep — per vitest config preamble).

import { describe, expect, test } from "vitest";

import { groupByDomain, statusForQuestion } from "./question-list";
import type { Question } from "./types";

function makeQuestion(over: Partial<Question> = {}): Question {
  return {
    id: "q1",
    code: "IAM-01",
    text: "Are users required to use MFA?",
    domain: "IAM",
    answer_type: "yes_no",
    scf_anchor_id: "IAC-06",
    sort_order: 1,
    needs_mapping: false,
    ...over,
  };
}

describe("statusForQuestion", () => {
  test("returns Unanswered when answer is missing", () => {
    expect(statusForQuestion(makeQuestion())).toBe("Unanswered");
  });

  test("returns Draft when answer_value is set", () => {
    expect(
      statusForQuestion(
        makeQuestion({
          answer: {
            id: "a1",
            answer_value: "Yes",
            narrative: "",
            citations: [],
          },
        }),
      ),
    ).toBe("Draft");
  });

  test("returns Draft when narrative is set", () => {
    expect(
      statusForQuestion(
        makeQuestion({
          answer: {
            id: "a1",
            answer_value: "",
            narrative: "We do.",
            citations: [],
          },
        }),
      ),
    ).toBe("Draft");
  });

  test("returns Unanswered for empty answer record", () => {
    expect(
      statusForQuestion(
        makeQuestion({
          answer: {
            id: "a1",
            answer_value: "",
            narrative: "",
            citations: [],
          },
        }),
      ),
    ).toBe("Unanswered");
  });
});

describe("groupByDomain", () => {
  test("groups questions by their domain field", () => {
    const groups = groupByDomain([
      makeQuestion({ id: "q1", domain: "IAM" }),
      makeQuestion({ id: "q2", domain: "DSI" }),
      makeQuestion({ id: "q3", domain: "IAM" }),
    ]);
    expect(groups).toHaveLength(2);
    const iam = groups.find((g) => g.domain === "IAM");
    expect(iam?.questions).toHaveLength(2);
  });

  test("treats empty domain as 'Other'", () => {
    const groups = groupByDomain([makeQuestion({ domain: "" })]);
    expect(groups[0].domain).toBe("Other");
  });

  test("sorts questions inside a group by sort_order", () => {
    const groups = groupByDomain([
      makeQuestion({ id: "q-late", sort_order: 10 }),
      makeQuestion({ id: "q-early", sort_order: 1 }),
    ]);
    expect(groups[0].questions[0].id).toBe("q-early");
    expect(groups[0].questions[1].id).toBe("q-late");
  });

  test("returns empty array for empty input", () => {
    expect(groupByDomain([])).toEqual([]);
  });
});
