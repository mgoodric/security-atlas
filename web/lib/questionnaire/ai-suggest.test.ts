// Slice 441 — unit tests for the AI-suggest view-model. The load-bearing
// assertion is the approve gate: a drafted suggestion is approvable ONLY with
// a resolved citation; every other outcome (insufficient / suppressed / error
// / malformed) is NOT approvable (P0-441-4, fail closed).

import { describe, expect, it } from "vitest";

import {
  type AISuggestResponse,
  parseAISuggest,
  toViewModel,
} from "./ai-suggest";

const drafted: AISuggestResponse = {
  answer_id: "11111111-1111-1111-1111-111111111111",
  question_id: "22222222-2222-2222-2222-222222222222",
  draft: "We encrypt data at rest (policy 33333333-...).",
  citations: [{ kind: "policy", id: "33333333-3333-3333-3333-333333333333" }],
  insufficient_evidence: false,
  suppressed: false,
  model_name: "llama3.1:8b-instruct-q5",
  model_version: "1",
  model_provider: "ollama-local",
  cloud_routed: false,
};

describe("toViewModel — drafted", () => {
  it("is approvable when a draft has a resolved citation", () => {
    const vm = toViewModel(drafted);
    expect(vm.status).toBe("drafted");
    expect(vm.canApprove).toBe(true);
    expect(vm.answerId).toBe(drafted.answer_id);
    expect(vm.citations).toHaveLength(1);
    expect(vm.message).toBe("");
    expect(vm.modelLabel).toContain("llama3.1");
    expect(vm.cloudRouted).toBe(false);
  });

  it("is NOT approvable when citations are empty (fail closed)", () => {
    const vm = toViewModel({ ...drafted, citations: [] });
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
  });

  it("is NOT approvable when the answer id is missing", () => {
    const vm = toViewModel({ ...drafted, answer_id: undefined });
    expect(vm.canApprove).toBe(false);
    expect(vm.status).toBe("error");
  });

  it("is NOT approvable when the draft text is missing", () => {
    const vm = toViewModel({ ...drafted, draft: undefined });
    expect(vm.canApprove).toBe(false);
  });
});

describe("toViewModel — insufficient evidence (AC-5)", () => {
  it("maps to a non-approvable insufficient state", () => {
    const vm = toViewModel({
      insufficient_evidence: true,
      reason: "insufficient_evidence",
    });
    expect(vm.status).toBe("insufficient");
    expect(vm.canApprove).toBe(false);
    expect(vm.message).toMatch(/answer manually/i);
  });
});

describe("toViewModel — suppressed (P0-441-4)", () => {
  it("suppressed takes precedence over a stray draft and is not approvable", () => {
    const vm = toViewModel({
      ...drafted,
      suppressed: true,
      reason: "unresolved_citation",
    });
    expect(vm.status).toBe("suppressed");
    expect(vm.canApprove).toBe(false);
    expect(vm.draft).toBe("");
    expect(vm.message).toMatch(/could not be verified/i);
  });

  it("maps each suppression reason to a message", () => {
    for (const reason of [
      "unresolved_citation",
      "no_citations",
      "generation_unavailable",
      "something_else",
    ]) {
      const vm = toViewModel({ suppressed: true, reason });
      expect(vm.canApprove).toBe(false);
      expect(vm.message.length).toBeGreaterThan(0);
    }
  });
});

describe("toViewModel — cloud routing banner", () => {
  it("flags cloud routing for the visible banner", () => {
    const vm = toViewModel({ ...drafted, cloud_routed: true });
    expect(vm.cloudRouted).toBe(true);
  });

  it("renders an empty model label when no model ran", () => {
    const vm = toViewModel({ insufficient_evidence: true });
    expect(vm.modelLabel).toBe("");
  });
});

describe("parseAISuggest — HTTP + malformed handling (fail closed)", () => {
  it("maps a non-OK status to a non-approvable error with the server message", () => {
    const vm = parseAISuggest(false, 403, { error: "grc_engineer required" });
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
    expect(vm.message).toBe("grc_engineer required");
  });

  it("maps a non-OK status with no error field to a generic message", () => {
    const vm = parseAISuggest(false, 500, {});
    expect(vm.canApprove).toBe(false);
    expect(vm.message).toContain("500");
  });

  it("maps a non-object body to a non-approvable error", () => {
    const vm = parseAISuggest(true, 200, "not json");
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
  });

  it("passes an OK drafted body through to the view-model", () => {
    const vm = parseAISuggest(true, 200, drafted);
    expect(vm.status).toBe("drafted");
    expect(vm.canApprove).toBe(true);
  });
});
