// Slice 440 — vitest for the board-narrative AI editor-mode view-model. Pure
// node logic; the load-bearing assertion is the editor-mode approval gate
// (AC-12, AC-20, P0-440-4): the operator cannot approve a section while a
// citation is unresolved (the server suppresses such a draft, so canApprove is
// false and the approve button never enables).

import { describe, expect, it } from "vitest";

import {
  approveEnabled,
  parseGenerate,
  toViewModel,
  type NarrativeGenerateResponse,
} from "./narrative-ai";

const drafted: NarrativeGenerateResponse = {
  record_id: "rec-1",
  section: "control_coverage_summary",
  draft: "## Control coverage summary\n1. Coverage 84%.",
  citations: [
    { kind: "control", id: "c1" },
    { kind: "evidence", id: "e1" },
  ],
  suppressed: false,
  model_name: "llama3.1",
  model_version: "8b",
  model_provider: "ollama-local",
  cloud_routed: false,
};

describe("toViewModel", () => {
  it("drafted with citations is approvable", () => {
    const vm = toViewModel(drafted);
    expect(vm.status).toBe("drafted");
    expect(vm.canApprove).toBe(true);
    expect(vm.citations).toHaveLength(2);
    expect(vm.recordId).toBe("rec-1");
    expect(vm.modelLabel).toBe("llama3.1 v8b (ollama-local)");
    expect(vm.cloudRouted).toBe(false);
  });

  it("drafted WITHOUT citations is NOT approvable (fail closed)", () => {
    const vm = toViewModel({ ...drafted, citations: [] });
    expect(vm.canApprove).toBe(false);
    expect(vm.status).toBe("error");
  });

  it("drafted without a record id is NOT approvable", () => {
    const vm = toViewModel({ ...drafted, record_id: undefined });
    expect(vm.canApprove).toBe(false);
  });

  it("drafted with empty draft text is NOT approvable", () => {
    const vm = toViewModel({ ...drafted, draft: "" });
    expect(vm.canApprove).toBe(false);
  });

  const reasons: Array<[string, RegExp]> = [
    ["numeric_mismatch", /number/i],
    ["unresolved_citation", /cited material/i],
    ["no_citations", /no citations/i],
    ["section_shape_violation", /structure/i],
    ["banned_phrase", /marketing language/i],
    ["generation_unavailable", /temporarily unavailable/i],
    ["something_else", /withheld/i],
  ];
  it.each(reasons)(
    "suppressed/%s is never approvable and explains the guardrail",
    (reason, msgRe) => {
      const vm = toViewModel({ suppressed: true, reason });
      expect(vm.status).toBe("suppressed");
      expect(vm.canApprove).toBe(false);
      expect(vm.draft).toBe("");
      expect(vm.message).toMatch(msgRe);
    },
  );

  it("suppressed takes precedence over a stray draft (fail closed)", () => {
    const vm = toViewModel({
      ...drafted,
      suppressed: true,
      reason: "numeric_mismatch",
    });
    expect(vm.canApprove).toBe(false);
    expect(vm.status).toBe("suppressed");
  });

  it("cloud routing surfaces the banner flag", () => {
    const vm = toViewModel({ ...drafted, cloud_routed: true });
    expect(vm.cloudRouted).toBe(true);
  });

  it("empty model produces an empty label", () => {
    const vm = toViewModel({ ...drafted, model_name: undefined });
    expect(vm.modelLabel).toBe("");
  });
});

describe("approveEnabled (the editor-mode gate)", () => {
  it("enabled when approvable and final text non-empty", () => {
    const vm = toViewModel(drafted);
    expect(approveEnabled(vm, "edited final text")).toBe(true);
  });

  it("disabled when the operator empties the text", () => {
    const vm = toViewModel(drafted);
    expect(approveEnabled(vm, "   ")).toBe(false);
  });

  it("disabled when the draft is not approvable (unresolved citation)", () => {
    const vm = toViewModel({ suppressed: true, reason: "unresolved_citation" });
    expect(approveEnabled(vm, "anything the operator types")).toBe(false);
  });
});

describe("parseGenerate", () => {
  it("non-OK status is an error state, never approvable", () => {
    const vm = parseGenerate(false, 403, { error: "forbidden" });
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
    expect(vm.message).toBe("forbidden");
  });

  it("non-OK status without an error field uses a generic message", () => {
    const vm = parseGenerate(false, 500, null);
    expect(vm.message).toMatch(/Generation failed \(500\)/);
  });

  it("malformed OK body is an error state", () => {
    const vm = parseGenerate(true, 200, "not an object");
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
  });

  it("well-formed OK body parses to the drafted view-model", () => {
    const vm = parseGenerate(true, 200, drafted);
    expect(vm.status).toBe("drafted");
    expect(vm.canApprove).toBe(true);
  });
});
