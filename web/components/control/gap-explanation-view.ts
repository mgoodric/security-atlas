// Slice 444 — pure view-model for the AI gap-explanation card.
//
// This module is the NODE-ENV-testable core of the gap-explanation surface
// (web/vitest.config.ts pins environment: "node", no JSX/DOM — slice 069
// P0-A3 / slice 353 Q-3). The React component (gap-explanation.tsx) is a thin
// renderer over this view-model; the Playwright e2e tier covers the rendered
// DOM. Keeping the decisions here (show explanation vs rollup-only, the
// disclosure text, the explicit absence of any approve/publish/export action)
// makes the NON-BINDING contract (AC-5/AC-6/AC-7, P0-444-3) unit-testable on
// the fast surface.

import type {
  ControlGapExplanationResponse,
  GapCitation,
} from "@/lib/api/control-detail";

// gapExplanationView is the decided render shape. It deliberately exposes NO
// action affordance: there is no approve/publish/export field, because the
// explanation is non-binding and read-only (AC-5, P0-444-3). The view is
// either "explanation present" (showExplanation=true) or "rollup only"
// (graceful degradation, AC-7).
export type GapExplanationView = {
  // Always true — the deterministic rollup renders in every state (AC-7).
  showRollup: true;
  // True only when a non-suppressed explanation is present.
  showExplanation: boolean;
  // The plain-language text (empty when !showExplanation).
  text: string;
  // The resolved, tenant-owned citations (empty when !showExplanation).
  citations: GapCitation[];
  // The visible non-audit-artifact disclosure naming the model (AC-6). Empty
  // when no explanation renders.
  disclosure: string;
  // A short, human-readable note shown when the explanation is withheld so the
  // operator understands they are seeing the deterministic rollup alone.
  degradedNote: string;
};

// suppressionNote maps the backend's fixed suppression-reason vocabulary to a
// short, honest, operator-facing note. The reason vocabulary is closed
// (mirrors internal/gapexplain reasons); an unknown reason falls back to a
// neutral default rather than echoing a raw string into the UI.
export function suppressionNote(reason: string): string {
  switch (reason) {
    case "generation_unavailable":
      return "The AI explanation is unavailable right now. Showing the underlying freshness facts.";
    case "unresolved_citation":
      return "The AI explanation was withheld because one of its citations could not be verified against your records. Showing the underlying freshness facts.";
    case "no_citations":
      return "The AI explanation was withheld because it cited no specific evidence. Showing the underlying freshness facts.";
    default:
      return "Showing the underlying freshness facts.";
  }
}

// buildGapExplanationView turns a backend response into the render shape. The
// explanation renders ONLY when the backend returned a non-null explanation
// (the backend already enforces citation validation + suppression — the
// frontend never re-decides bindingness, it just reflects the contract).
export function buildGapExplanationView(
  resp: ControlGapExplanationResponse,
): GapExplanationView {
  const exp = resp.explanation;
  if (exp && exp.text.length > 0) {
    return {
      showRollup: true,
      showExplanation: true,
      text: exp.text,
      citations: exp.citations,
      // The backend supplies the canonical disclosure string; we surface it
      // verbatim so the model name + "not an audit artifact" notice are
      // exactly what the backend recorded (AC-6).
      disclosure: exp.disclosure,
      degradedNote: "",
    };
  }
  return {
    showRollup: true,
    showExplanation: false,
    text: "",
    citations: [],
    disclosure: "",
    degradedNote: suppressionNote(resp.suppressed_reason),
  };
}

// formatRollupSummary builds the one-line deterministic gap summary that
// always renders (AC-7). Numbers come straight from the rollup — never from
// the model.
export function formatRollupSummary(
  resp: ControlGapExplanationResponse,
): string {
  const r = resp.rollup;
  const state = r.is_stale ? "in a freshness gap (stale)" : "fresh";
  const cls = r.freshness_class ? ` · class ${r.freshness_class}` : "";
  return `${r.evidence_count} evidence record${
    r.evidence_count === 1 ? "" : "s"
  } in window · ${state}${cls}`;
}
