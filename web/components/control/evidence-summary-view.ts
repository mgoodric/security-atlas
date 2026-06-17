// Slice 502 — pure view-model for the AI evidence-summary card.
//
// This module is the NODE-ENV-testable core of the evidence-summary surface
// (web/vitest.config.ts pins environment: "node", no JSX/DOM — slice 069 P0-A3
// / slice 353 Q-3). The React component (evidence-summary.tsx) is a thin
// renderer over this view-model; the Playwright e2e tier covers the rendered
// DOM. Keeping the decisions here (show summary vs evidence-list-only, the
// disclosure text, the explicit absence of any approve/publish/export action)
// makes the NON-BINDING contract (AC-5/AC-6/AC-7, P0-502-3) unit-testable on the
// fast surface. Mirrors slice 444's gap-explanation-view.ts.

import type {
  ControlEvidenceSummaryResponse,
  EvidenceSummaryCitation,
} from "@/lib/api/control-detail";

// evidenceSummaryView is the decided render shape. It deliberately exposes NO
// action affordance: there is no approve/publish/export field, because the
// summary is non-binding and read-only (AC-5, P0-502-3). The view is either
// "summary present" (showSummary=true) or "evidence-list only" (graceful
// degradation, AC-7).
export type EvidenceSummaryView = {
  // Always true — the deterministic evidence list renders in every state (AC-7).
  showEvidence: true;
  // True only when a non-suppressed summary is present.
  showSummary: boolean;
  // The plain-language text (empty when !showSummary).
  text: string;
  // The resolved, tenant-owned citations (empty when !showSummary).
  citations: EvidenceSummaryCitation[];
  // The visible non-audit-artifact disclosure naming the model (AC-6). Empty
  // when no summary renders.
  disclosure: string;
  // A short, human-readable note shown when the summary is withheld so the
  // operator understands they are seeing the deterministic evidence list alone.
  degradedNote: string;
};

// suppressionNote maps the backend's fixed suppression-reason vocabulary to a
// short, honest, operator-facing note. The reason vocabulary is closed (mirrors
// internal/evidencesummary reasons); an unknown reason falls back to a neutral
// default rather than echoing a raw string into the UI.
export function suppressionNote(reason: string): string {
  switch (reason) {
    case "generation_unavailable":
      return "The AI summary is unavailable right now. Showing the underlying evidence records.";
    case "unresolved_citation":
      return "The AI summary was withheld because one of its citations could not be verified against your records. Showing the underlying evidence records.";
    case "no_citations":
      return "The AI summary was withheld because it cited no specific evidence. Showing the underlying evidence records.";
    case "no_evidence":
      return "There is no current live evidence to summarize for this control yet.";
    default:
      return "Showing the underlying evidence records.";
  }
}

// buildEvidenceSummaryView turns a backend response into the render shape. The
// summary renders ONLY when the backend returned a non-null summary (the backend
// already enforces citation validation + suppression — the frontend never
// re-decides bindingness, it just reflects the contract).
export function buildEvidenceSummaryView(
  resp: ControlEvidenceSummaryResponse,
): EvidenceSummaryView {
  const sum = resp.summary;
  if (sum && sum.text.length > 0) {
    return {
      showEvidence: true,
      showSummary: true,
      text: sum.text,
      citations: sum.citations,
      // The backend supplies the canonical disclosure string; we surface it
      // verbatim so the model name + "not an audit artifact" notice are exactly
      // what the backend recorded (AC-6).
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

// formatEvidenceBound builds the one-line deterministic "showing N of M live
// records" label that always renders (AC-7). Numbers come straight from the
// evidence set — never from the model — and the live-only marker reflects
// P0-502-5.
export function formatEvidenceBound(
  resp: ControlEvidenceSummaryResponse,
): string {
  const ev = resp.evidence;
  const recordWord = ev.total === 1 ? "record" : "records";
  const liveTag = ev.live_only ? " (current live evidence only)" : "";
  if (ev.total > ev.showing) {
    return `Summarizing the ${ev.showing} most-recent of ${ev.total} live evidence ${recordWord}${liveTag}`;
  }
  return `${ev.total} live evidence ${recordWord}${liveTag}`;
}
