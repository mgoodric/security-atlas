// Slice 444 — vitest for the gap-explanation view-model (AC-11). Node env, no
// JSX/DOM (web/vitest.config.ts P0-A3): the view-model carries the
// NON-BINDING render decisions, so the disclosure / no-export / graceful-
// degradation contract is covered here on the fast surface. The rendered DOM
// is the Playwright e2e tier's concern.

import { describe, expect, it } from "vitest";

import type { ControlGapExplanationResponse } from "@/lib/api/control-detail";
import {
  buildGapExplanationView,
  formatRollupSummary,
  suppressionNote,
} from "./gap-explanation-view";

const CONTROL_ID = "11111111-1111-1111-1111-111111111111";
const EVIDENCE_ID = "22222222-2222-2222-2222-222222222222";

function baseRollup(): ControlGapExplanationResponse["rollup"] {
  return {
    control_id: CONTROL_ID,
    control_title: "Quarterly access reviews",
    freshness_class: "quarterly",
    is_stale: true,
    evidence_count: 1,
    latest_observed_at: "2026-02-01T00:00:00Z",
    valid_until: "2026-05-01T00:00:00Z",
    evidence: [
      {
        evidence_id: EVIDENCE_ID,
        evidence_kind: "access_review.completion",
        result: "pass",
        observed_at: "2026-02-01T00:00:00Z",
      },
    ],
  };
}

function withExplanation(): ControlGapExplanationResponse {
  return {
    control_id: CONTROL_ID,
    rollup: baseRollup(),
    explanation: {
      text: `Control (${CONTROL_ID}) is in a freshness gap; its latest evidence (${EVIDENCE_ID}) is past the quarterly window.`,
      citations: [
        { kind: "control", id: CONTROL_ID },
        { kind: "evidence", id: EVIDENCE_ID },
      ],
      model: "llama3.1:8b-instruct-q5 1",
      model_name: "llama3.1:8b-instruct-q5",
      model_version: "1",
      model_provider: "ollama-local",
      binding: false,
      disclosure:
        "AI-generated explanation (model llama3.1:8b-instruct-q5 1) — not an audit artifact.",
    },
    suppressed_reason: "",
  };
}

function suppressed(reason: string): ControlGapExplanationResponse {
  return {
    control_id: CONTROL_ID,
    rollup: baseRollup(),
    explanation: null,
    suppressed_reason: reason,
  };
}

describe("buildGapExplanationView", () => {
  it("renders the explanation with its disclosure when present (AC-6)", () => {
    const view = buildGapExplanationView(withExplanation());
    expect(view.showRollup).toBe(true);
    expect(view.showExplanation).toBe(true);
    expect(view.text).toContain(CONTROL_ID);
    expect(view.citations).toHaveLength(2);
    // AC-6: the disclosure names the model AND marks it not-an-audit-artifact.
    expect(view.disclosure).toContain("not an audit artifact");
    expect(view.disclosure).toContain("llama3.1");
  });

  it("exposes NO approve/publish/export affordance (AC-5, P0-444-3)", () => {
    const view = buildGapExplanationView(withExplanation());
    // The view shape is the entire UI contract; assert it carries no action
    // key that could publish/approve/export the non-binding explanation.
    const keys = Object.keys(view);
    for (const forbidden of [
      "approve",
      "publish",
      "export",
      "approvalUrl",
      "publishUrl",
      "exportUrl",
      "binding",
    ]) {
      expect(keys).not.toContain(forbidden);
    }
  });

  it("falls back to the rollup with a note when suppressed (AC-7)", () => {
    const view = buildGapExplanationView(suppressed("unresolved_citation"));
    expect(view.showRollup).toBe(true);
    expect(view.showExplanation).toBe(false);
    expect(view.text).toBe("");
    expect(view.citations).toHaveLength(0);
    expect(view.disclosure).toBe("");
    expect(view.degradedNote.length).toBeGreaterThan(0);
  });

  it("treats an empty-text explanation as suppressed", () => {
    const resp = withExplanation();
    resp.explanation!.text = "";
    const view = buildGapExplanationView(resp);
    expect(view.showExplanation).toBe(false);
  });
});

describe("suppressionNote", () => {
  it("maps the closed reason vocabulary to honest copy", () => {
    expect(suppressionNote("generation_unavailable")).toContain("unavailable");
    expect(suppressionNote("unresolved_citation")).toContain(
      "could not be verified",
    );
    expect(suppressionNote("no_citations")).toContain(
      "cited no specific evidence",
    );
  });

  it("falls back to a neutral note for an unknown reason (no raw echo)", () => {
    const note = suppressionNote("something_unexpected");
    expect(note).toBe("Showing the underlying freshness facts.");
    expect(note).not.toContain("something_unexpected");
  });
});

describe("formatRollupSummary", () => {
  it("states the deterministic facts, pluralized correctly", () => {
    const s = formatRollupSummary(withExplanation());
    expect(s).toContain("1 evidence record ");
    expect(s).toContain("freshness gap");
    expect(s).toContain("quarterly");
  });

  it("reports fresh when not stale", () => {
    const resp = withExplanation();
    resp.rollup.is_stale = false;
    resp.rollup.evidence_count = 3;
    const s = formatRollupSummary(resp);
    expect(s).toContain("3 evidence records");
    expect(s).toContain("fresh");
  });
});
