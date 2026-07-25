// Slice 502 — vitest for the evidence-summary view-model (AC-11). Node env, no
// JSX/DOM (web/vitest.config.ts P0-A3): the view-model carries the NON-BINDING
// render decisions, so the disclosure / no-export / graceful-degradation
// contract is covered here on the fast surface. The rendered DOM is the
// Playwright e2e tier's concern. Mirrors slice 444's gap-explanation-view test.

import { describe, expect, it } from "vitest";

import type { ControlEvidenceSummaryResponse } from "@/lib/api/control-detail";
import {
  buildEvidenceSummaryView,
  formatEvidenceBound,
  suppressionNote,
} from "./evidence-summary-view";

const CONTROL_ID = "11111111-1111-1111-1111-111111111111";
const EVIDENCE_ID = "22222222-2222-2222-2222-222222222222";

function baseEvidence(
  overrides: Partial<ControlEvidenceSummaryResponse["evidence"]> = {},
): ControlEvidenceSummaryResponse["evidence"] {
  return {
    control_id: CONTROL_ID,
    control_title: "Quarterly access reviews",
    showing: 1,
    total: 1,
    live_only: true,
    records: [
      {
        evidence_id: EVIDENCE_ID,
        evidence_kind: "access_review.completion",
        result: "pass",
        observed_at: "2026-02-01T00:00:00Z",
      },
    ],
    ...overrides,
  };
}

function withSummary(): ControlEvidenceSummaryResponse {
  return {
    control_id: CONTROL_ID,
    evidence: baseEvidence(),
    summary: {
      text: `The evidence (${EVIDENCE_ID}) shows the quarterly access review passed for control (${CONTROL_ID}).`,
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

function suppressed(reason: string): ControlEvidenceSummaryResponse {
  return {
    control_id: CONTROL_ID,
    evidence: baseEvidence(),
    summary: null,
    suppressed_reason: reason,
  };
}

describe("buildEvidenceSummaryView", () => {
  it("renders the summary with its disclosure when present (AC-6)", () => {
    const view = buildEvidenceSummaryView(withSummary());
    expect(view.showEvidence).toBe(true);
    expect(view.showSummary).toBe(true);
    expect(view.text).toContain(CONTROL_ID);
    expect(view.citations).toHaveLength(2);
    // AC-6: the disclosure names the model AND marks it not-an-audit-artifact.
    expect(view.disclosure).toContain("not an audit artifact");
    expect(view.disclosure).toContain("llama3.1");
  });

  it("exposes NO approve/publish/export affordance (AC-5, P0-502-3)", () => {
    const view = buildEvidenceSummaryView(withSummary());
    // The view shape is the entire UI contract; assert it carries no action key
    // that could publish/approve/export the non-binding summary.
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

  it("falls back to the evidence list with a note when suppressed (AC-7)", () => {
    const view = buildEvidenceSummaryView(suppressed("unresolved_citation"));
    expect(view.showEvidence).toBe(true);
    expect(view.showSummary).toBe(false);
    expect(view.text).toBe("");
    expect(view.citations).toHaveLength(0);
    expect(view.disclosure).toBe("");
    expect(view.degradedNote.length).toBeGreaterThan(0);
  });

  it("treats an empty-text summary as suppressed", () => {
    const resp = withSummary();
    resp.summary!.text = "";
    const view = buildEvidenceSummaryView(resp);
    expect(view.showSummary).toBe(false);
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
    expect(suppressionNote("no_evidence")).toContain(
      "no current live evidence",
    );
  });

  it("falls back to a neutral note for an unknown reason (no raw echo)", () => {
    const note = suppressionNote("something_unexpected");
    expect(note).toBe("Showing the underlying evidence records.");
    expect(note).not.toContain("something_unexpected");
  });
});

describe("formatEvidenceBound", () => {
  it("labels current-live-only and the bound when history exceeds it (P0-502-5/8)", () => {
    const resp = withSummary();
    resp.evidence = baseEvidence({ showing: 8, total: 25 });
    const s = formatEvidenceBound(resp);
    expect(s).toContain("8 most-recent of 25");
    expect(s).toContain("current live evidence only");
  });

  it("states the plain count when the whole live set fits the bound", () => {
    const resp = withSummary();
    resp.evidence = baseEvidence({ showing: 1, total: 1 });
    const s = formatEvidenceBound(resp);
    expect(s).toContain("1 live evidence record");
    expect(s).not.toContain("most-recent of");
  });
});
