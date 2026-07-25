// Slice 749 — vitest for the period-scoped (frozen) evidence-summary view-model.
// Node env, no JSX/DOM (web/vitest.config.ts P0-A3): the view-model carries the
// NON-BINDING render decisions + the period-scoped/frozen-as-of label, so the
// disclosure / no-export / graceful-degradation / frozen-label contract is
// covered here on the fast surface. The rendered DOM is the Playwright e2e tier's
// concern. Mirrors slice 502's evidence-summary-view test.

import { describe, expect, it } from "vitest";

import type { PeriodEvidenceSummaryResponse } from "@/lib/api/control-detail";
import {
  buildPeriodEvidenceSummaryView,
  formatFrozenAsOf,
  formatFrozenEvidenceBound,
  suppressionNote,
} from "./period-evidence-summary-view";

const PERIOD_ID = "33333333-3333-3333-3333-333333333333";
const CONTROL_ID = "11111111-1111-1111-1111-111111111111";
const EVIDENCE_ID = "22222222-2222-2222-2222-222222222222";

function baseEvidence(
  overrides: Partial<PeriodEvidenceSummaryResponse["evidence"]> = {},
): PeriodEvidenceSummaryResponse["evidence"] {
  return {
    control_id: CONTROL_ID,
    control_title: "Quarterly access reviews",
    audit_period_id: PERIOD_ID,
    frozen_at: "2026-03-31T23:59:59Z",
    frozen: true,
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
    ...overrides,
  };
}

function withSummary(): PeriodEvidenceSummaryResponse {
  return {
    audit_period_id: PERIOD_ID,
    control_id: CONTROL_ID,
    evidence: baseEvidence(),
    summary: {
      text: `The frozen evidence (${EVIDENCE_ID}) shows the access review passed for control (${CONTROL_ID}).`,
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

function suppressed(reason: string): PeriodEvidenceSummaryResponse {
  return {
    audit_period_id: PERIOD_ID,
    control_id: CONTROL_ID,
    evidence: baseEvidence(),
    summary: null,
    suppressed_reason: reason,
  };
}

describe("buildPeriodEvidenceSummaryView", () => {
  it("renders the summary with its disclosure when present (AC-4)", () => {
    const view = buildPeriodEvidenceSummaryView(withSummary());
    expect(view.showEvidence).toBe(true);
    expect(view.showSummary).toBe(true);
    expect(view.text).toContain(CONTROL_ID);
    expect(view.citations).toHaveLength(2);
    expect(view.disclosure).toContain("not an audit artifact");
    expect(view.disclosure).toContain("llama3.1");
  });

  it("exposes NO approve/publish/export affordance (AC-4, P0-502-3)", () => {
    const view = buildPeriodEvidenceSummaryView(withSummary());
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

  it("falls back to the frozen evidence list with a note when suppressed (AC-7)", () => {
    const view = buildPeriodEvidenceSummaryView(
      suppressed("unresolved_citation"),
    );
    expect(view.showEvidence).toBe(true);
    expect(view.showSummary).toBe(false);
    expect(view.text).toBe("");
    expect(view.citations).toHaveLength(0);
    expect(view.disclosure).toBe("");
    expect(view.degradedNote).toContain("frozen");
  });

  it("treats an empty-text summary as suppressed", () => {
    const resp = withSummary();
    resp.summary!.text = "";
    const view = buildPeriodEvidenceSummaryView(resp);
    expect(view.showSummary).toBe(false);
  });
});

describe("suppressionNote (period-scoped)", () => {
  it("maps the closed reason vocabulary to honest frozen-population copy", () => {
    expect(suppressionNote("generation_unavailable")).toContain("unavailable");
    expect(suppressionNote("unresolved_citation")).toContain(
      "frozen population",
    );
    expect(suppressionNote("no_citations")).toContain(
      "cited no specific evidence",
    );
    expect(suppressionNote("no_evidence")).toContain("frozen evidence");
  });

  it("falls back to a neutral frozen note for an unknown reason (no raw echo)", () => {
    const note = suppressionNote("something_unexpected");
    expect(note).toBe("Showing the underlying frozen evidence records.");
    expect(note).not.toContain("something_unexpected");
  });
});

describe("formatFrozenAsOf", () => {
  it("renders the load-bearing period-scoped + frozen-as-of label (AC-4)", () => {
    const s = formatFrozenAsOf(withSummary());
    expect(s).toContain("Period-scoped");
    expect(s).toContain("frozen as of 2026-03-31");
  });

  it("degrades to a neutral label when frozen_at is missing", () => {
    const resp = withSummary();
    // @ts-expect-error — exercise the defensive empty-string branch.
    resp.evidence.frozen_at = "";
    expect(formatFrozenAsOf(resp)).toContain("the freeze date");
  });
});

describe("formatFrozenEvidenceBound", () => {
  it("labels the bound when the frozen history exceeds it", () => {
    const resp = withSummary();
    resp.evidence = baseEvidence({ showing: 8, total: 25 });
    const s = formatFrozenEvidenceBound(resp);
    expect(s).toContain("8 most-recent of 25 frozen evidence records");
  });

  it("states the plain count when the whole frozen set fits the bound", () => {
    const resp = withSummary();
    resp.evidence = baseEvidence({ showing: 1, total: 1 });
    const s = formatFrozenEvidenceBound(resp);
    expect(s).toContain("1 frozen evidence record");
    expect(s).not.toContain("most-recent of");
  });
});
