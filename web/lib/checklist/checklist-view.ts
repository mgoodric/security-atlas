// Slice 471 — pure view-model for the role-scoped control-implementation
// checklist surface.
//
// This module is the NODE-ENV-testable core (web/vitest.config.ts pins
// environment: "node", no JSX/DOM — slice 069 P0-A3 / slice 353 Q-3). The React
// page is a thin renderer over this view-model; the Playwright e2e tier covers
// the rendered DOM. Keeping the decisions here — which sections are approvable,
// the non-binding disclosure, the suppression notes, the approved-vs-draft
// gating of the export button — makes the NON-BINDING + one-click-approval
// contract (AC-9..AC-12, P0-471-1) unit-testable on the fast surface.

// CitationView is one resolved reference an item makes.
export type CitationView = {
  kind: "control" | "scf_anchor" | "policy" | string;
  id: string;
  ref?: string;
};

export type ItemView = {
  control_id: string;
  control_title?: string;
  task: string;
  no_evidence: boolean;
  citations: CitationView[];
};

export type SectionView = {
  section_id?: string;
  role: string;
  ai_assisted: boolean;
  human_approved: boolean;
  human_approver?: string;
  suppressed: boolean;
  reason?: string;
  model_name?: string;
  model_version?: string;
  model_provider?: string;
  cloud_routed: boolean;
  items: ItemView[];
};

export type ChecklistResponse = {
  generation_id: string;
  sections: SectionView[];
  cloud_routed: boolean;
  binding: boolean;
  disclosure: string;
};

// roleLabel maps the fixed v0 role token to an operator-facing label.
export function roleLabel(role: string): string {
  switch (role) {
    case "infra":
      return "Infrastructure team";
    case "engineering":
      return "Engineering team";
    case "security":
      return "Security team";
    case "unassigned":
      return "Unassigned controls";
    default:
      return role;
  }
}

// suppressionNote maps the backend's closed suppression-reason vocabulary to a
// short, honest, operator-facing note. An unknown reason falls back to a neutral
// default rather than echoing a raw string into the UI (slice-367 leak
// discipline mirrored on the frontend).
export function suppressionNote(reason: string | undefined): string {
  switch (reason) {
    case "generation_unavailable":
      return "The AI draft for this team is unavailable right now. Try generating again.";
    case "unresolved_citation":
      return "This team's draft was withheld because one of its task citations could not be verified against your controls.";
    case "no_citations":
      return "This team's draft was withheld because a task cited no specific control.";
    case "no_tasks":
      return "The model produced no usable tasks for this team.";
    default:
      return "This team's draft was withheld.";
  }
}

// SectionRenderState is the decided render shape for one role section.
export type SectionRenderState = {
  sectionId: string;
  roleLabel: string;
  // True only for an AI-authored, non-suppressed section the operator can act
  // on. The unassigned bucket + suppressed sections are NOT approvable.
  approvable: boolean;
  approved: boolean;
  approver: string;
  suppressed: boolean;
  note: string;
  // Model provenance disclosure (empty for the unassigned bucket / suppressed).
  modelDisclosure: string;
  items: ItemView[];
};

// buildSectionState turns one backend section into its render state. The
// approvable flag is the single source of truth for whether the per-section
// Approve button renders (AC-10): only an ai_assisted, non-suppressed,
// not-yet-approved section is approvable.
export function buildSectionState(s: SectionView): SectionRenderState {
  const approvable = s.ai_assisted && !s.suppressed && !s.human_approved;
  let modelDisclosure = "";
  if (s.ai_assisted && !s.suppressed && s.model_name) {
    const ver = s.model_version ? ` ${s.model_version}` : "";
    modelDisclosure = `AI-drafted with model ${s.model_name}${ver} — review before use.`;
  }
  return {
    sectionId: s.section_id ?? "",
    roleLabel: roleLabel(s.role),
    approvable,
    approved: s.human_approved,
    approver: s.human_approver ?? "",
    suppressed: s.suppressed,
    note: s.suppressed ? suppressionNote(s.reason) : "",
    modelDisclosure,
    items: s.items,
  };
}

// canExport reports whether the markdown export button should be enabled: at
// least one AI section is approved (AC-11, P0-471-1). A draft with zero approved
// sections cannot be exported, so the button is disabled until an approval
// lands.
export function canExport(resp: ChecklistResponse): boolean {
  return resp.sections.some(
    (s) => s.ai_assisted && s.human_approved && !s.suppressed,
  );
}

// approvedCount counts the approved AI sections — drives the "N of M approved"
// progress affordance.
export function approvedCount(resp: ChecklistResponse): number {
  return resp.sections.filter(
    (s) => s.ai_assisted && s.human_approved && !s.suppressed,
  ).length;
}

// aiSectionCount counts the AI sections (excludes the unassigned bucket) — the
// denominator of the approval progress.
export function aiSectionCount(resp: ChecklistResponse): number {
  return resp.sections.filter((s) => s.ai_assisted).length;
}

// showCloudBanner reports whether the visible cloud-routing banner should show.
// Always false in v0 (local Ollama only, P0-471-5); the helper exists so the
// cloud opt-in follow-on flips it without a render change.
export function showCloudBanner(resp: ChecklistResponse): boolean {
  return resp.cloud_routed;
}

// draftDisclosure is the fallback non-binding disclosure if the backend did not
// supply one (it always does, but the frontend never renders an empty notice).
export const draftDisclosure =
  "AI-assisted draft — review before use. Not an audit artifact until each section is approved.";

// disclosureText returns the backend disclosure, or the local fallback.
export function disclosureText(resp: ChecklistResponse): string {
  return resp.disclosure && resp.disclosure.length > 0
    ? resp.disclosure
    : draftDisclosure;
}
