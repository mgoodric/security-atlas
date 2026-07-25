// Slice 749 — pure view-model for the period-scoped (frozen) evidence-summary
// card in the audit workspace.
//
// This module is the NODE-ENV-testable core of the period-scoped surface
// (web/vitest.config.ts pins environment: "node", no JSX/DOM — slice 069 P0-A3 /
// slice 353 Q-3). The React component (period-evidence-summary.tsx) is a thin
// renderer over this view-model; the Playwright e2e tier covers the rendered DOM.
// Keeping the decisions here (show summary vs frozen-evidence-list-only, the
// frozen-as-of label, the disclosure text, the explicit absence of any
// approve/publish/export action) makes the NON-BINDING + frozen-population
// contract (AC-2/AC-4/AC-7, P0-502-3) unit-testable on the fast surface. Mirrors
// slice 502's evidence-summary-view.ts; the ONLY additions are the
// period-scoped + frozen-as-of label.

import type {
  EvidenceSummaryCitation,
  PeriodEvidenceSummaryResponse,
} from "@/lib/api/control-detail";

export type PeriodEvidenceSummaryView = {
  // Always true — the deterministic frozen evidence list renders in every state
  // (AC-7).
  showEvidence: true;
  // True only when a non-suppressed summary is present.
  showSummary: boolean;
  // The plain-language text (empty when !showSummary).
  text: string;
  // The resolved, tenant-owned, frozen-population citations (empty when
  // !showSummary).
  citations: EvidenceSummaryCitation[];
  // The visible non-audit-artifact disclosure naming the model (AC-4). Empty
  // when no summary renders.
  disclosure: string;
  // A short, human-readable note shown when the summary is withheld so the
  // operator understands they are seeing the deterministic frozen evidence list
  // alone.
  degradedNote: string;
};

// suppressionNote maps the backend's fixed suppression-reason vocabulary to a
// short, honest, operator-facing note. The reason vocabulary is closed (mirrors
// internal/evidencesummary reasons); an unknown reason falls back to a neutral
// default rather than echoing a raw string into the UI. The "no_evidence" copy
// is frozen-population specific.
export function suppressionNote(reason: string): string {
  switch (reason) {
    case "generation_unavailable":
      return "The AI summary is unavailable right now. Showing the underlying frozen evidence records.";
    case "unresolved_citation":
      return "The AI summary was withheld because one of its citations could not be verified against the frozen population. Showing the underlying frozen evidence records.";
    case "no_citations":
      return "The AI summary was withheld because it cited no specific evidence. Showing the underlying frozen evidence records.";
    case "no_evidence":
      return "There is no frozen evidence in this audit period for this control.";
    default:
      return "Showing the underlying frozen evidence records.";
  }
}

export function buildPeriodEvidenceSummaryView(
  resp: PeriodEvidenceSummaryResponse,
): PeriodEvidenceSummaryView {
  const sum = resp.summary;
  if (sum && sum.text.length > 0) {
    return {
      showEvidence: true,
      showSummary: true,
      text: sum.text,
      citations: sum.citations,
      disclosure: sum.disclosure,
      degradedNote: "",
    };
  }
  return {
    showEvidence: true,
    showSummary: false,
    text: "",
    citations: [],
    disclosure: "",
    degradedNote: suppressionNote(resp.suppressed_reason),
  };
}

// formatFrozenAsOf builds the load-bearing "period-scoped, frozen as of <date>"
// label that always renders (AC-4). The date is the period freeze horizon the
// corpus is bounded by (observed_at <= frozen_at — invariant #10). We render the
// UTC date portion for a stable, locale-independent label.
export function formatFrozenAsOf(resp: PeriodEvidenceSummaryResponse): string {
  const iso = resp.evidence.frozen_at;
  const day = iso ? iso.slice(0, 10) : "the freeze date";
  return `Period-scoped · frozen as of ${day}`;
}

// formatFrozenEvidenceBound builds the one-line deterministic "showing N of M
// frozen records" label that always renders (AC-7). Numbers come straight from
// the frozen evidence set — never from the model.
export function formatFrozenEvidenceBound(
  resp: PeriodEvidenceSummaryResponse,
): string {
  const ev = resp.evidence;
  const recordWord = ev.total === 1 ? "record" : "records";
  if (ev.total > ev.showing) {
    return `Summarizing the ${ev.showing} most-recent of ${ev.total} frozen evidence ${recordWord}`;
  }
  return `${ev.total} frozen evidence ${recordWord}`;
}
