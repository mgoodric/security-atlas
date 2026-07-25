// Slice 440 — view-model for the board-narrative AI editor-mode affordance
// (guardrail 7). Pure, node-testable logic that interprets the platform's
// board-narrative generate response and decides the editor panel's display
// state + the approve-button gate. Kept out of the .tsx component so the
// constitutional gate ("the operator cannot approve a section with an
// unresolved citation" — AC-12, P0-440-4) is unit-tested, not buried in JSX.
//
// The platform SectionResult shape (internal/boardnarrative, JSON-tagged) is
// one of:
//   - drafted:    { record_id, draft, citations:[...], suppressed:false,
//                   model_*, cloud_routed }
//   - suppressed: { suppressed:true, reason:"numeric_mismatch"|"unresolved_citation"
//                   |"section_shape_violation"|"banned_phrase"|... }
//
// Unlike the questionnaire surface there is no "insufficient evidence" outcome:
// the rollup is always computable when a brief exists, and the suppression
// reasons are the four guardrail rejections.

export interface NarrativeCitation {
  kind: "control" | "evidence";
  id: string;
}

export interface NarrativeGenerateResponse {
  record_id?: string;
  section?: string;
  draft?: string;
  citations?: NarrativeCitation[];
  suppressed?: boolean;
  reason?: string;
  model_name?: string;
  model_version?: string;
  model_provider?: string;
  cloud_routed?: boolean;
}

export type NarrativeStatus = "drafted" | "suppressed" | "error";

export interface NarrativeViewModel {
  status: NarrativeStatus;
  // The draft text the operator edits before approving. Empty unless drafted.
  draft: string;
  // The resolved, tenant-owned citations backing the draft. Every entry is
  // proven server-side (guardrail 4); the FE renders them, it does not
  // re-validate.
  citations: NarrativeCitation[];
  // The persisted draft record's id — needed to approve. Empty unless drafted.
  recordId: string;
  // True ONLY when the operator may approve: a drafted section with a persisted
  // record id, draft text, AND at least one resolved citation. This is the FE
  // mirror of the server's mandatory-citation gate — the approve button is
  // DISABLED otherwise (AC-12, P0-440-4: never approve a section without
  // resolved citation backing).
  canApprove: boolean;
  // A short, human-readable banner message for non-drafted outcomes.
  message: string;
  // True when the generation was routed to a cloud LLM — the UI renders a
  // visible routing banner (CLAUDE.md inference-backend rule). False in v0.
  cloudRouted: boolean;
  // Model provenance for the transparency line. Empty when no model ran.
  modelLabel: string;
}

// suppressedMessage maps a guardrail-rejection reason to operator-facing copy.
// The board narrative is the highest-risk surface, so the messages name the
// guardrail that fired (the operator should understand WHY the AI draft was
// withheld and that the deterministic numbers are still available via the
// templated brief).
function suppressedMessage(reason: string | undefined): string {
  switch (reason) {
    case "numeric_mismatch":
      return "The AI draft stated a number that did not match the verified rollup and was withheld. Write the section manually.";
    case "unresolved_citation":
      return "The AI draft cited material that could not be verified and was withheld. Write the section manually.";
    case "no_citations":
      return "The AI draft had no citations and was withheld. Write the section manually.";
    case "section_shape_violation":
      return "The AI draft did not follow the required section structure and was withheld. Write the section manually.";
    case "banned_phrase":
      return "The AI draft used marketing language not permitted in board narratives and was withheld. Write the section manually.";
    case "generation_unavailable":
      return "Board-narrative AI is temporarily unavailable. Write the section manually or try again.";
    default:
      return "The AI draft was withheld. Write the section manually.";
  }
}

function modelLabel(r: NarrativeGenerateResponse): string {
  if (!r.model_name) return "";
  const provider = r.model_provider ? ` (${r.model_provider})` : "";
  const version = r.model_version ? ` v${r.model_version}` : "";
  return `${r.model_name}${version}${provider}`;
}

// toViewModel maps a parsed generate response into the editor panel's
// view-model. Suppressed takes precedence over drafted so a malformed response
// that sets both flags fails CLOSED (never shows an approvable draft it should
// not).
export function toViewModel(r: NarrativeGenerateResponse): NarrativeViewModel {
  const cloudRouted = r.cloud_routed === true;
  const label = modelLabel(r);

  if (r.suppressed === true) {
    return {
      status: "suppressed",
      draft: "",
      citations: [],
      recordId: "",
      canApprove: false,
      message: suppressedMessage(r.reason),
      cloudRouted,
      modelLabel: label,
    };
  }

  const citations = Array.isArray(r.citations) ? r.citations : [];
  const draft = typeof r.draft === "string" ? r.draft : "";
  const recordId = typeof r.record_id === "string" ? r.record_id : "";

  // A drafted section is only approvable when it has a persisted record id,
  // draft text, AND at least one resolved citation. The server already enforces
  // this (it never persists a draft that failed the citation gate), but the FE
  // gate is defense-in-depth: a response missing any of these is NOT approvable
  // (fail closed — AC-12, P0-440-4).
  const canApprove =
    recordId.length > 0 && draft.length > 0 && citations.length > 0;

  return {
    status: canApprove ? "drafted" : "error",
    draft,
    citations,
    recordId,
    canApprove,
    message: canApprove
      ? ""
      : "The AI response was incomplete and cannot be approved. Write the section manually.",
    cloudRouted,
    modelLabel: label,
  };
}

// approveEnabled is the explicit editor-mode gate the .tsx binds the approve
// button's `disabled` to. The operator can approve ONLY when the view-model is
// approvable AND the (possibly edited) final text is non-empty. An operator who
// deletes all the text cannot approve an empty section. This is the AC-12
// "cannot approve while any citation is unresolved" gate made concrete: an
// unresolved citation means canApprove=false (the server suppressed the draft),
// so the button never enables.
export function approveEnabled(
  vm: NarrativeViewModel,
  editedText: string,
): boolean {
  return vm.canApprove && editedText.trim().length > 0;
}

// parseGenerate parses a raw fetch response into a view-model, mapping a non-OK
// HTTP status or unparseable body to a safe "error" state (never approvable).
// Centralizes the FE's fail-closed posture.
export function parseGenerate(
  ok: boolean,
  status: number,
  raw: unknown,
): NarrativeViewModel {
  if (!ok) {
    let msg = `Generation failed (${status}).`;
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
      recordId: "",
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
      recordId: "",
      canApprove: false,
      message: "The AI response was malformed and cannot be approved.",
      cloudRouted: false,
      modelLabel: "",
    };
  }
  return toViewModel(raw as NarrativeGenerateResponse);
}
