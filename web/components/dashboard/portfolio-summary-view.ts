// Slice 750 — pure view-model for the portfolio (multi-control) AI
// evidence-summary card.
//
// This module is the NODE-ENV-testable core of the portfolio-summary surface
// (web/vitest.config.ts pins environment: "node" — slice 069 P0-A3 / slice 353
// Q-3). The React component (portfolio-summary.tsx) is a thin renderer over this
// view-model; the Playwright e2e tier covers the rendered DOM. Keeping the
// decisions here — show summary vs rollup-only, the disclosure text, BOTH bound
// labels, the explicit absence of any approve/publish/export action — makes the
// NON-BINDING + two-level-bounded contract (AC-5, P0-750-2, P0-502-3)
// unit-testable on the fast surface. Mirrors slice 502's evidence-summary-view.ts.

import type { EvidenceSummaryCitation } from "@/lib/api/control-detail";
import type { PortfolioEvidenceSummaryResponse } from "@/lib/api/portfolio-summary";

// PortfolioSummaryView is the decided render shape. It deliberately exposes NO
// action affordance: there is no approve/publish/export field, because the
// summary is non-binding and read-only (AC-5, P0-502-3). The view is either
// "summary present" or "rollup only" (graceful degradation, AC-7).
export type PortfolioSummaryView = {
  // Always true — the deterministic rollup renders in every state (AC-7).
  showRollup: true;
  // True only when a non-suppressed summary is present.
  showSummary: boolean;
  // The plain-language text (empty when !showSummary).
  text: string;
  // The resolved, tenant-owned cross-control citations (empty when !showSummary).
  citations: EvidenceSummaryCitation[];
  // The visible non-audit-artifact disclosure naming the model (AC-6). Empty when
  // no summary renders.
  disclosure: string;
  // A short, human-readable note shown when the summary is withheld so the
  // operator understands they are seeing the deterministic rollup alone.
  degradedNote: string;
};

// suppressionNote maps the backend's fixed suppression-reason vocabulary to a
// short, honest, operator-facing note. The reason vocabulary is closed (mirrors
// internal/evidencesummary reasons); an unknown reason falls back to a neutral
// default. Includes the portfolio-specific numeric_mismatch reason (AC-3).
export function suppressionNote(reason: string): string {
  switch (reason) {
    case "generation_unavailable":
      return "The AI summary is unavailable right now. Showing the underlying rollup.";
    case "unresolved_citation":
      return "The AI summary was withheld because one of its citations could not be verified against your records. Showing the underlying rollup.";
    case "no_citations":
      return "The AI summary was withheld because it cited no specific evidence. Showing the underlying rollup.";
    case "no_evidence":
      return "There is no current live evidence to summarize across these controls yet.";
    case "numeric_mismatch":
      return "The AI summary was withheld because one of its counts did not match the deterministic rollup. Showing the underlying rollup.";
    default:
      return "Showing the underlying rollup.";
  }
}

// buildPortfolioSummaryView turns a backend response into the render shape. The
// summary renders ONLY when the backend returned a non-null summary (the backend
// already enforces citation validation, numeric verification, and suppression —
// the frontend never re-decides bindingness, it just reflects the contract).
export function buildPortfolioSummaryView(
  resp: PortfolioEvidenceSummaryResponse,
): PortfolioSummaryView {
  const sum = resp.summary;
  if (sum && sum.text.length > 0) {
    return {
      showRollup: true,
      showSummary: true,
      text: sum.text,
      citations: sum.citations,
      disclosure: sum.disclosure,
      degradedNote: "",
    };
  }
  return {
    showRollup: true,
    showSummary: false,
    text: "",
    citations: [],
    disclosure: "",
    degradedNote: suppressionNote(resp.suppressed_reason),
  };
}

// formatPortfolioScope describes the filtered control set in one honest phrase
// (AC-5). The scope comes straight from the deterministic envelope, never the
// model.
export function formatPortfolioScope(
  resp: PortfolioEvidenceSummaryResponse,
): string {
  const ev = resp.evidence;
  switch (ev.mode) {
    case "framework":
      return `Framework: ${ev.framework_label ?? "selected framework"}`;
    case "family":
      return `Control family: ${ev.family ?? ""}`.trim();
    default:
      return "Whole program";
  }
}

// formatPortfolioBounds builds the one-line BOTH-bounds honesty label that always
// renders (AC-5, P0-750-2). It states the controls-per-summary bound ("K most-
// relevant of N controls") AND the records-per-control bound ("up to M records
// each"). Numbers come straight from the deterministic rollup — never from the
// model — and the live-only marker reflects P0-502-5.
export function formatPortfolioBounds(
  resp: PortfolioEvidenceSummaryResponse,
): string {
  const ev = resp.evidence;
  const r = ev.rollup;
  const controlWord = r.total_matched === 1 ? "control" : "controls";
  const liveTag = ev.live_only ? " (current live evidence only)" : "";
  const recordsClause = `up to ${ev.records_per_control} records each`;
  if (r.total_matched > r.controls_in_summary) {
    return `Summarizing the ${r.controls_in_summary} most-relevant of ${r.total_matched} ${controlWord}; ${recordsClause}${liveTag}`;
  }
  return `${r.controls_in_summary} ${controlWord}; ${recordsClause}${liveTag}`;
}

// formatPortfolioRollupLine builds the deterministic coverage line ("X of Y
// controls have current live evidence; Z gaps") from ground-truth counts — the
// numbers the AI summary was numeric-verified against (AC-3). Always rendered.
export function formatPortfolioRollupLine(
  resp: PortfolioEvidenceSummaryResponse,
): string {
  const r = resp.evidence.rollup;
  return `${r.controls_with_evidence} of ${r.controls_in_summary} controls have current live evidence; ${r.controls_without_evidence} with none; ${r.total_records} records total`;
}
