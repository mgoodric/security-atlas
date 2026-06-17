// Slice 750 — vitest for the portfolio-summary view-model. Node env, no JSX/DOM
// (web/vitest.config.ts P0-A3): the view-model carries the NON-BINDING +
// two-level-bounded render decisions, so the disclosure / BOTH-bound-labels /
// no-export / graceful-degradation contract is covered here on the fast surface.
// The rendered DOM is the Playwright e2e tier's concern. Mirrors slice 502's
// evidence-summary-view test.

import { describe, expect, it } from "vitest";

import type { PortfolioEvidenceSummaryResponse } from "@/lib/api/portfolio-summary";
import {
  buildPortfolioSummaryView,
  formatPortfolioBounds,
  formatPortfolioRollupLine,
  formatPortfolioScope,
  suppressionNote,
} from "./portfolio-summary-view";

const CONTROL_ID = "11111111-1111-1111-1111-111111111111";
const EVIDENCE_ID = "22222222-2222-2222-2222-222222222222";

function baseEvidence(
  overrides: Partial<PortfolioEvidenceSummaryResponse["evidence"]> = {},
): PortfolioEvidenceSummaryResponse["evidence"] {
  return {
    mode: "family",
    family: "IAC",
    live_only: true,
    controls_per_summary: 12,
    records_per_control: 4,
    rollup: {
      controls_in_summary: 3,
      total_matched: 30,
      controls_with_evidence: 2,
      controls_without_evidence: 1,
      total_records: 5,
    },
    controls: [
      {
        control_id: CONTROL_ID,
        control_title: "Quarterly access reviews",
        showing: 1,
        total: 1,
        records: [
          {
            evidence_id: EVIDENCE_ID,
            evidence_kind: "access_review.completion",
            result: "pass",
            observed_at: "2026-02-01T00:00:00Z",
          },
        ],
      },
    ],
    ...overrides,
  };
}

function withSummary(): PortfolioEvidenceSummaryResponse {
  return {
    evidence: baseEvidence(),
    summary: {
      text: `Across 3 controls, 2 have current live evidence; control (${CONTROL_ID}) cites (${EVIDENCE_ID}).`,
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
        "AI-generated summary (model llama3.1:8b-instruct-q5 1) — not an audit artifact.",
    },
    suppressed_reason: "",
  };
}

function suppressed(reason: string): PortfolioEvidenceSummaryResponse {
  return {
    evidence: baseEvidence(),
    summary: null,
    suppressed_reason: reason,
  };
}

describe("buildPortfolioSummaryView", () => {
  it("renders the summary with the disclosure when present", () => {
    const view = buildPortfolioSummaryView(withSummary());
    expect(view.showRollup).toBe(true);
    expect(view.showSummary).toBe(true);
    expect(view.text).toContain(CONTROL_ID);
    expect(view.citations).toHaveLength(2);
    expect(view.disclosure).toContain("not an audit artifact");
    expect(view.disclosure).toContain("llama3.1");
  });

  it("falls back to rollup-only with an honest note on suppression", () => {
    const view = buildPortfolioSummaryView(suppressed("numeric_mismatch"));
    expect(view.showRollup).toBe(true);
    expect(view.showSummary).toBe(false);
    expect(view.text).toBe("");
    expect(view.citations).toHaveLength(0);
    expect(view.degradedNote.toLowerCase()).toContain("count");
  });

  it("exposes NO approve/publish/export affordance in the view shape", () => {
    const view = buildPortfolioSummaryView(withSummary());
    expect(view).not.toHaveProperty("approve");
    expect(view).not.toHaveProperty("publish");
    expect(view).not.toHaveProperty("export");
  });
});

describe("formatPortfolioBounds (BOTH bounds — AC-5/P0-750-2)", () => {
  it("states the controls-per-summary AND records-per-control bounds", () => {
    const label = formatPortfolioBounds(withSummary());
    // First level: K of N controls.
    expect(label).toContain("3 most-relevant of 30 controls");
    // Second level: up to M records each.
    expect(label).toContain("up to 4 records each");
    // Live-only honesty.
    expect(label).toContain("current live evidence only");
  });

  it("drops the 'of N' clause when the set was not capped", () => {
    const resp = suppressed("");
    resp.evidence.rollup.total_matched = 3;
    resp.evidence.rollup.controls_in_summary = 3;
    const label = formatPortfolioBounds(resp);
    expect(label).not.toContain("most-relevant of");
    expect(label).toContain("up to 4 records each");
  });
});

describe("formatPortfolioScope", () => {
  it("names the control-family scope", () => {
    expect(formatPortfolioScope(withSummary())).toBe("Control family: IAC");
  });

  it("names the framework scope", () => {
    const resp = suppressed("");
    resp.evidence.mode = "framework";
    resp.evidence.framework_label = "SOC 2 (2017)";
    expect(formatPortfolioScope(resp)).toBe("Framework: SOC 2 (2017)");
  });

  it("names the whole-program scope", () => {
    const resp = suppressed("");
    resp.evidence.mode = "program";
    expect(formatPortfolioScope(resp)).toBe("Whole program");
  });
});

describe("formatPortfolioRollupLine (deterministic, ground-truth)", () => {
  it("renders the coverage counts from the rollup, not the model", () => {
    const line = formatPortfolioRollupLine(withSummary());
    expect(line).toContain("2 of 3 controls have current live evidence");
    expect(line).toContain("1 with none");
    expect(line).toContain("5 records total");
  });
});

describe("suppressionNote", () => {
  it("maps numeric_mismatch to a count-specific note (AC-3)", () => {
    expect(suppressionNote("numeric_mismatch").toLowerCase()).toContain(
      "count",
    );
  });
  it("maps generation_unavailable to an availability note", () => {
    expect(suppressionNote("generation_unavailable").toLowerCase()).toContain(
      "unavailable",
    );
  });
  it("maps unresolved_citation to a verification note", () => {
    expect(suppressionNote("unresolved_citation").toLowerCase()).toContain(
      "verified",
    );
  });
  it("maps no_citations to a cited-no-evidence note", () => {
    expect(suppressionNote("no_citations").toLowerCase()).toContain(
      "cited no specific evidence",
    );
  });
  it("maps no_evidence to a no-live-evidence note", () => {
    expect(suppressionNote("no_evidence").toLowerCase()).toContain(
      "no current live evidence",
    );
  });
  it("maps unknown reasons to a neutral default", () => {
    expect(suppressionNote("totally_unknown")).toBe(
      "Showing the underlying rollup.",
    );
  });
});

describe("formatPortfolioBounds singular/edge", () => {
  it("uses the singular 'control' when only one matched", () => {
    const resp = suppressed("");
    resp.evidence.rollup.total_matched = 1;
    resp.evidence.rollup.controls_in_summary = 1;
    resp.evidence.live_only = false;
    const label = formatPortfolioBounds(resp);
    expect(label).toContain("1 control;");
    expect(label).not.toContain("current live evidence only");
  });
});

describe("formatPortfolioScope fallbacks", () => {
  it("falls back to a generic framework label when absent", () => {
    const resp = suppressed("");
    resp.evidence.mode = "framework";
    resp.evidence.framework_label = undefined;
    expect(formatPortfolioScope(resp)).toBe("Framework: selected framework");
  });
});
