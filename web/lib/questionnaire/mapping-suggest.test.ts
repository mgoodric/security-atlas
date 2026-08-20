import { describe, expect, it } from "vitest";

import {
  parseMappingSuggest,
  type MappingSuggestResponse,
  toMappingViewModel,
} from "./mapping-suggest";

const proposed: MappingSuggestResponse = {
  proposal_id: "11111111-1111-1111-1111-111111111111",
  question_id: "22222222-2222-2222-2222-222222222222",
  scf_anchor_id: "IAC-06",
  title: "Multi-Factor Authentication",
  rationale: "The question asks about MFA for access control.",
  model_name: "llama3.1",
  model_version: "1",
  model_provider: "ollama-local",
};

describe("toMappingViewModel", () => {
  it("makes a complete proposal approvable", () => {
    const vm = toMappingViewModel(proposed);
    expect(vm.status).toBe("proposed");
    expect(vm.canApprove).toBe(true);
    expect(vm.scfAnchorId).toBe("IAC-06");
    expect(vm.modelLabel).toContain("ollama-local");
  });

  it("fails closed when proposal id is missing", () => {
    const vm = toMappingViewModel({ ...proposed, proposal_id: undefined });
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
  });

  it("maps no candidates to manual mapping", () => {
    const vm = toMappingViewModel({
      manual_only: true,
      reason: "no_candidates",
    });
    expect(vm.status).toBe("manual");
    expect(vm.canApprove).toBe(false);
    expect(vm.message).toMatch(/map manually/i);
  });

  it("maps suppression to non-approvable withheld state", () => {
    const vm = toMappingViewModel({
      ...proposed,
      suppressed: true,
      reason: "out_of_grounding",
    });
    expect(vm.status).toBe("suppressed");
    expect(vm.canApprove).toBe(false);
    expect(vm.scfAnchorId).toBe("");
  });

  it("honors cloud routing flag", () => {
    expect(
      toMappingViewModel({ ...proposed, cloud_routed: true }).cloudRouted,
    ).toBe(true);
  });
});

describe("parseMappingSuggest", () => {
  it("maps upstream errors to non-approvable errors", () => {
    const vm = parseMappingSuggest(false, 403, { error: "forbidden" });
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
    expect(vm.message).toBe("forbidden");
  });

  it("maps malformed success to non-approvable errors", () => {
    const vm = parseMappingSuggest(true, 200, "bad");
    expect(vm.status).toBe("error");
    expect(vm.canApprove).toBe(false);
  });
});
