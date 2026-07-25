// Slice 441 — view-model for the AI-answer suggestion affordance.
//
// Pure, node-testable logic that interprets the platform's ai-suggest response
// and decides the panel's display state + the approve-button gate. Kept out of
// the .tsx component so the constitutional gate ("operator cannot approve a
// draft with an unresolved citation" — AC-11/AC-18, P0-441-4) is unit-tested,
// not buried in JSX.
//
// The platform Suggestion shape (internal/qaisuggest, JSON-tagged) is one of:
//   - drafted:      { answer_id, draft, citations:[...], suppressed:false,
//                     insufficient_evidence:false, model_*, cloud_routed }
//   - insufficient: { insufficient_evidence:true, reason:"insufficient_evidence" }
//   - suppressed:   { suppressed:true, reason:"unresolved_citation"|... }

export interface AISuggestCitation {
  kind: "policy" | "evidence";
  id: string;
}

export interface AISuggestResponse {
  answer_id?: string;
  question_id?: string;
  draft?: string;
  citations?: AISuggestCitation[];
  insufficient_evidence?: boolean;
  suppressed?: boolean;
  reason?: string;
  model_name?: string;
  model_version?: string;
  model_provider?: string;
  cloud_routed?: boolean;
}

export type AISuggestStatus =
  | "drafted"
  | "insufficient"
  | "suppressed"
  | "error";

export interface AISuggestViewModel {
  status: AISuggestStatus;
  // The draft text the operator edits before approving. Empty unless drafted.
  draft: string;
  // The resolved, tenant-owned citations backing the draft. Every entry is
  // proven server-side (AC-4); the FE renders them, it does not re-validate.
  citations: AISuggestCitation[];
  // The persisted draft answer's id — needed to approve. Empty unless drafted.
  answerId: string;
  // True ONLY when the operator may approve: a drafted suggestion with at
  // least one resolved citation. This is the FE mirror of the server's
  // mandatory-citation gate — the approve button is disabled otherwise
  // (P0-441-4: never approve a draft without resolved citation backing).
  canApprove: boolean;
  // A short, human-readable banner message for non-drafted outcomes.
  message: string;
  // True when the generation was routed to a cloud LLM — the UI renders a
  // visible routing banner (CLAUDE.md inference-backend rule). False in v0.
  cloudRouted: boolean;
  // Model provenance for the transparency line (AC-8). Empty when no model ran.
  modelLabel: string;
}

const INSUFFICIENT_MESSAGE =
  "Insufficient evidence to draft an answer — answer manually.";

function suppressedMessage(reason: string | undefined): string {
  switch (reason) {
    case "unresolved_citation":
      return "The AI draft cited material that could not be verified and was withheld. Answer manually.";
    case "no_citations":
      return "The AI draft had no citations and was withheld. Answer manually.";
    case "generation_unavailable":
      return "AI suggestion is temporarily unavailable. Answer manually or try again.";
    default:
      return "The AI draft was withheld. Answer manually.";
  }
}

function modelLabel(r: AISuggestResponse): string {
  if (!r.model_name) return "";
  const provider = r.model_provider ? ` (${r.model_provider})` : "";
  const version = r.model_version ? ` v${r.model_version}` : "";
  return `${r.model_name}${version}${provider}`;
}

// toViewModel maps a parsed ai-suggest response into the panel's view-model.
// The three server outcomes are mutually exclusive; this resolves them in a
// fixed precedence (suppressed > insufficient > drafted) so a malformed
// response that sets multiple flags fails CLOSED (never shows an approvable
// draft it should not).
export function toViewModel(r: AISuggestResponse): AISuggestViewModel {
  const cloudRouted = r.cloud_routed === true;
  const label = modelLabel(r);

  if (r.suppressed === true) {
    return {
      status: "suppressed",
      draft: "",
      citations: [],
      answerId: "",
      canApprove: false,
      message: suppressedMessage(r.reason),
      cloudRouted,
      modelLabel: label,
    };
  }

  if (r.insufficient_evidence === true) {
    return {
      status: "insufficient",
      draft: "",
      citations: [],
      answerId: "",
      canApprove: false,
      message: INSUFFICIENT_MESSAGE,
      cloudRouted,
      modelLabel: label,
    };
  }

  const citations = Array.isArray(r.citations) ? r.citations : [];
  const draft = typeof r.draft === "string" ? r.draft : "";
  const answerId = typeof r.answer_id === "string" ? r.answer_id : "";

  // A drafted suggestion is only approvable when it has a persisted answer id,
  // draft text, AND at least one resolved citation. The server already enforces
  // this (it never persists a draft that failed the citation gate), but the FE
  // gate is defense-in-depth: a response missing any of these is NOT
  // approvable (fail closed — P0-441-4).
  const canApprove =
    answerId.length > 0 && draft.length > 0 && citations.length > 0;

  return {
    status: canApprove ? "drafted" : "error",
    draft,
    citations,
    answerId,
    canApprove,
    message: canApprove
      ? ""
      : "The AI response was incomplete and cannot be approved. Answer manually.",
    cloudRouted,
    modelLabel: label,
  };
}

// parseAISuggest parses a raw fetch response body into a view-model, mapping a
// non-OK HTTP status or unparseable body to a safe "error" state (never
// approvable). Centralizes the FE's fail-closed posture.
export function parseAISuggest(
  ok: boolean,
  status: number,
  raw: unknown,
): AISuggestViewModel {
  if (!ok) {
    let msg = `Suggestion failed (${status}).`;
    if (
      raw &&
      typeof raw === "object" &&
      typeof (raw as { error?: string }).error === "string"
    ) {
      msg = (raw as { error: string }).error;
    }
    return {
      status: "error",
      draft: "",
      citations: [],
      answerId: "",
      canApprove: false,
      message: msg,
      cloudRouted: false,
      modelLabel: "",
    };
  }
  if (!raw || typeof raw !== "object") {
    return {
      status: "error",
      draft: "",
      citations: [],
      answerId: "",
      canApprove: false,
      message: "The AI response was malformed and cannot be approved.",
      cloudRouted: false,
      modelLabel: "",
    };
  }
  return toViewModel(raw as AISuggestResponse);
}
